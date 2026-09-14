package control

import (
	"testing"
	"time"
)

func TestMarkSeenBatchBaselinesWholeFirstSnapshot(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := s.MarkSeenBatch("account-a", []string{"account-a:one", "account-a:two"})
	if first["account-a:one"] || first["account-a:two"] {
		t.Fatal("initial snapshot must establish a quiet baseline")
	}
	second := s.MarkSeenBatch("account-a", []string{"account-a:one", "account-a:two", "account-a:three"})
	if !second["account-a:three"] {
		t.Fatal("new task after a baseline should be reported")
	}
	if second["account-a:one"] || second["account-a:two"] {
		t.Fatal("previous tasks must not be reported again")
	}
}

func TestSetAutomationPersistsIntervalAndRejectsInvalidValue(t *testing.T) {
	dir := t.TempDir()
	s, err := NewState(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.SetAutomation("checkin", true, 90)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Enabled || a.EveryMinute != 90 || a.NextRunAt.IsZero() {
		t.Fatalf("unexpected saved automation: %#v", a)
	}
	if _, err := s.SetAutomation("checkin", true, 4); err == nil {
		t.Fatal("invalid interval was accepted")
	}
	reloaded, err := NewState(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range reloaded.Automations() {
		if item.ID == "checkin" && item.EveryMinute != 90 {
			t.Fatalf("expected persisted 90-minute interval, got %d", item.EveryMinute)
		}
	}
}

func TestRunsReturnsEmptyArrayBeforeFirstRun(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runs := s.Runs()
	if runs == nil || len(runs) != 0 {
		t.Fatalf("expected a non-nil empty run list, got %#v", runs)
	}
}

func TestMockSchedulerTaskLifecyclePersistsLocally(t *testing.T) {
	dir := t.TempDir()
	s, err := NewState(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateMockSchedulerTask("account-a", "每日摘要", "0 9 * * *", "只用于本地 Mock", true)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || !created.Enabled || created.AccountUID != "account-a" {
		t.Fatalf("unexpected created task: %#v", created)
	}
	updated, err := s.UpdateMockSchedulerTask(created.ID, "晚间摘要", "0 20 * * *", "更新后的本地内容", false)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "晚间摘要" || updated.Enabled || updated.Cron != "0 20 * * *" {
		t.Fatalf("unexpected updated task: %#v", updated)
	}
	reloaded, err := NewState(dir)
	if err != nil {
		t.Fatal(err)
	}
	items := reloaded.MockSchedulerTasks()
	if len(items) != 1 || items[0].ID != created.ID || items[0].Name != "晚间摘要" {
		t.Fatalf("task was not persisted: %#v", items)
	}
	if err := reloaded.DeleteMockSchedulerTask(created.ID); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.MockSchedulerTasks(); len(got) != 0 {
		t.Fatalf("task was not deleted: %#v", got)
	}
}

func TestClaimDueReservesNextSlotBeforeExecution(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.mu.Lock()
	a := s.doc.Automations["activity-probe"]
	a.NextRunAt = now.Add(-time.Second)
	s.doc.Automations[a.ID] = a
	s.mu.Unlock()

	claimed := s.ClaimDue(now)
	if len(claimed) != 1 || claimed[0].ID != "activity-probe" {
		t.Fatalf("expected activity probe to be claimed once, got %#v", claimed)
	}
	if again := s.ClaimDue(now.Add(time.Second)); len(again) != 0 {
		t.Fatalf("claimed automation must not be claimed again before completion: %#v", again)
	}
	if err := s.Complete("activity-probe", "ok", true); err != nil {
		t.Fatal(err)
	}
	for _, item := range s.Automations() {
		if item.ID == "activity-probe" && !item.NextRunAt.After(now) {
			t.Fatalf("next slot was not retained after completion: %#v", item)
		}
	}
}

func TestMockSchedulerTaskRejectsInvalidInput(t *testing.T) {
	s, err := NewState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMockSchedulerTask("account-a", "", "0 9 * * *", "", true); err == nil {
		t.Fatal("empty name was accepted")
	}
	if _, err := s.CreateMockSchedulerTask("account-a", "任务", "", "", true); err == nil {
		t.Fatal("empty cron was accepted")
	}
}
