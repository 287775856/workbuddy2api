package control

import "testing"

func TestRedactTaskDetailRemovesSecretsRecursively(t *testing.T) {
	got := redactTaskDetail(map[string]any{
		"name": "daily task", "accessToken": "must-not-leak",
		"nested": map[string]any{"cookie": "must-not-leak", "enabled": true},
	})
	if _, ok := got["accessToken"]; ok {
		t.Fatal("token field leaked")
	}
	nested, ok := got["nested"].(map[string]any)
	if !ok || nested["enabled"] != true {
		t.Fatal("safe nested data missing")
	}
	if _, ok := nested["cookie"]; ok {
		t.Fatal("nested cookie field leaked")
	}
}
