package control

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"workbuddy-control-center/internal/fsutil"
)

// Automation is panel-owned scheduling metadata. It stores no credentials and
// no credit ledger.
type Automation struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Action      string    `json:"action"`
	Enabled     bool      `json:"enabled"`
	EveryMinute int       `json:"every_minutes"`
	LastRunAt   time.Time `json:"last_run_at,omitempty"`
	LastResult  string    `json:"last_result,omitempty"`
	NextRunAt   time.Time `json:"next_run_at,omitempty"`
}

type RunRecord struct {
	At      time.Time `json:"at"`
	Action  string    `json:"action"`
	OK      bool      `json:"ok"`
	Message string    `json:"message"`
}

// MockSchedulerTask is a local-only task draft used while the upstream
// scheduler API is unavailable. It is never submitted to WorkBuddy.
type MockSchedulerTask struct {
	ID         string    `json:"id"`
	AccountUID string    `json:"account_uid"`
	Name       string    `json:"name"`
	Cron       string    `json:"cron"`
	Prompt     string    `json:"prompt"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type persistentState struct {
	Automations    map[string]Automation        `json:"automations"`
	SeenTasks      map[string]time.Time         `json:"seen_tasks"`
	ProbedAccounts map[string]bool              `json:"probed_accounts"`
	Runs           []RunRecord                  `json:"runs"`
	MockTasks      map[string]MockSchedulerTask `json:"mock_scheduler_tasks"`
}

type State struct {
	mu   sync.Mutex
	path string
	doc  persistentState
}

func NewState(dataDir string) (*State, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	s := &State{path: filepath.Join(dataDir, "state.json")}
	s.doc.Automations = map[string]Automation{}
	s.doc.SeenTasks = map[string]time.Time{}
	s.doc.ProbedAccounts = map[string]bool{}
	s.doc.MockTasks = map[string]MockSchedulerTask{}
	if raw, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(raw, &s.doc)
	}
	if s.doc.Automations == nil {
		s.doc.Automations = map[string]Automation{}
	}
	if s.doc.SeenTasks == nil {
		s.doc.SeenTasks = map[string]time.Time{}
	}
	if s.doc.ProbedAccounts == nil {
		s.doc.ProbedAccounts = map[string]bool{}
	}
	if s.doc.MockTasks == nil {
		s.doc.MockTasks = map[string]MockSchedulerTask{}
	}
	if len(s.doc.Automations) == 0 {
		for _, a := range []Automation{
			{ID: "activity-probe", Name: "新活动探测", Action: "activity_probe", Enabled: true, EveryMinute: 30},
			{ID: "checkin", Name: "每日签到", Action: "checkin", Enabled: false, EveryMinute: 1440},
			{ID: "travel", Name: "旅行巡检", Action: "travel", Enabled: false, EveryMinute: 120},
			{ID: "keepalive", Name: "账号保活", Action: "refresh", Enabled: false, EveryMinute: 720},
		} {
			if a.Enabled {
				a.NextRunAt = time.Now().Add(time.Duration(a.EveryMinute) * time.Minute)
			}
			s.doc.Automations[a.ID] = a
		}
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *State) saveLocked() error {
	raw, err := json.MarshalIndent(s.doc, "", "  ")
	if err != nil {
		return err
	}
	_, err = fsutil.WriteFileAtomic(s.path, raw, 0o600)
	return err
}

func (s *State) Automations() []Automation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Automation, 0, len(s.doc.Automations))
	for _, v := range s.doc.Automations {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *State) SetAutomation(id string, enabled bool, every int) (Automation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.doc.Automations[id]
	if !ok {
		return Automation{}, os.ErrNotExist
	}
	if every < 5 || every > 7*24*60 {
		return Automation{}, fmt.Errorf("执行间隔需在 5 至 %d 分钟之间", 7*24*60)
	}
	a.EveryMinute = every
	a.Enabled = enabled
	if enabled {
		a.NextRunAt = time.Now().Add(time.Duration(a.EveryMinute) * time.Minute)
	} else {
		a.NextRunAt = time.Time{}
	}
	s.doc.Automations[id] = a
	return a, s.saveLocked()
}

// ClaimDue reserves due automations before they run. Reserving the next slot
// persistently prevents a slow upstream call from being started again by a
// later ticker cycle.
func (s *State) ClaimDue(now time.Time) []Automation {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []Automation
	for id, a := range s.doc.Automations {
		if !a.Enabled || (!a.NextRunAt.IsZero() && a.NextRunAt.After(now)) {
			continue
		}
		due = append(due, a)
		a.NextRunAt = now.Add(time.Duration(a.EveryMinute) * time.Minute)
		s.doc.Automations[id] = a
	}
	if len(due) > 0 {
		_ = s.saveLocked()
	}
	return due
}

func (s *State) Complete(id, result string, ok bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, found := s.doc.Automations[id]
	if !found {
		return os.ErrNotExist
	}
	now := time.Now()
	a.LastRunAt = now
	a.LastResult = result
	s.doc.Automations[id] = a
	s.doc.Runs = append([]RunRecord{{At: now, Action: a.Action, OK: ok, Message: result}}, s.doc.Runs...)
	if len(s.doc.Runs) > 100 {
		s.doc.Runs = s.doc.Runs[:100]
	}
	return s.saveLocked()
}

// MarkSeenBatch records an account's complete task snapshot. Only keys that
// appear after a completed baseline are returned as new. Processing a whole
// snapshot at once avoids incorrectly flagging the second item in a first scan.
func (s *State) MarkSeenBatch(accountUID string, keys []string) map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	wasProbed := s.doc.ProbedAccounts[accountUID]
	found := make(map[string]bool, len(keys))
	changed := false
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, exists := s.doc.SeenTasks[key]; exists {
			continue
		}
		s.doc.SeenTasks[key] = time.Now()
		found[key] = wasProbed
		changed = true
	}
	s.doc.ProbedAccounts[accountUID] = true
	if changed || !wasProbed {
		_ = s.saveLocked()
	}
	return found
}

func (s *State) Runs() []RunRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(make([]RunRecord, 0, len(s.doc.Runs)), s.doc.Runs...)
}

func validateMockSchedulerTask(name, cron, prompt string) error {
	if len([]rune(name)) == 0 || len([]rune(name)) > 100 {
		return fmt.Errorf("任务名称需为 1 至 100 个字符")
	}
	if len([]rune(cron)) == 0 || len([]rune(cron)) > 100 {
		return fmt.Errorf("调度表达式需为 1 至 100 个字符")
	}
	if len([]rune(prompt)) > 4000 {
		return fmt.Errorf("任务内容不能超过 4000 个字符")
	}
	return nil
}

func (s *State) MockSchedulerTasks() []MockSchedulerTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]MockSchedulerTask, 0, len(s.doc.MockTasks))
	for _, task := range s.doc.MockTasks {
		out = append(out, task)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (s *State) CreateMockSchedulerTask(accountUID, name, cron, prompt string, enabled bool) (MockSchedulerTask, error) {
	if err := validateMockSchedulerTask(name, cron, prompt); err != nil {
		return MockSchedulerTask{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	task := MockSchedulerTask{ID: fmt.Sprintf("mock-%d", now.UnixNano()), AccountUID: accountUID, Name: name, Cron: cron, Prompt: prompt, Enabled: enabled, CreatedAt: now, UpdatedAt: now}
	s.doc.MockTasks[task.ID] = task
	return task, s.saveLocked()
}

func (s *State) UpdateMockSchedulerTask(id, name, cron, prompt string, enabled bool) (MockSchedulerTask, error) {
	if err := validateMockSchedulerTask(name, cron, prompt); err != nil {
		return MockSchedulerTask{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.doc.MockTasks[id]
	if !ok {
		return MockSchedulerTask{}, os.ErrNotExist
	}
	task.Name, task.Cron, task.Prompt, task.Enabled = name, cron, prompt, enabled
	task.UpdatedAt = time.Now()
	s.doc.MockTasks[id] = task
	return task, s.saveLocked()
}

func (s *State) DeleteMockSchedulerTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.doc.MockTasks[id]; !ok {
		return os.ErrNotExist
	}
	delete(s.doc.MockTasks, id)
	return s.saveLocked()
}
