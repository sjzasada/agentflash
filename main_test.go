package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyHooks_FreshFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	url := "http://127.0.0.1:7777/api/claude/event"
	cmd := "curl -fsS -X POST " + url + " -d @-"

	changed, err := applyHooks(path, buildHooksMap(cmd), url)
	if err != nil {
		t.Fatalf("applyHooks: %v", err)
	}
	if !changed {
		t.Errorf("changed should be true on fresh file")
	}
	got := readJSON(t, path)
	hooks := got["hooks"].(map[string]any)
	for _, evt := range []string{"PreToolUse", "PostToolUse", "UserPromptSubmit", "Stop"} {
		entries := hooks[evt].([]any)
		if len(entries) != 1 {
			t.Errorf("%s: want 1 entry, got %d", evt, len(entries))
		}
	}
}

func TestApplyHooks_PreservesExistingUnrelatedHooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	url := "http://127.0.0.1:7777/api/claude/event"
	cmd := "curl -fsS -X POST " + url + " -d @-"

	existing := map[string]any{
		"theme": "dark",
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"type": "command", "command": "echo unrelated"},
					},
				},
			},
		},
	}
	writeJSON(t, path, existing)

	if _, err := applyHooks(path, buildHooksMap(cmd), url); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, path)
	if got["theme"] != "dark" {
		t.Errorf("theme not preserved: %v", got["theme"])
	}
	pre := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse: want 2 entries (1 unrelated + 1 ours), got %d: %+v", len(pre), pre)
	}
}

func TestApplyHooks_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	url := "http://127.0.0.1:7777/api/claude/event"
	cmd := "curl -fsS -X POST " + url + " -d @-"

	first, err := applyHooks(path, buildHooksMap(cmd), url)
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Errorf("first apply should change")
	}
	second, err := applyHooks(path, buildHooksMap(cmd), url)
	if err != nil {
		t.Fatal(err)
	}
	if second {
		t.Errorf("second apply should be a no-op")
	}
	pre := readJSON(t, path)["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Errorf("PreToolUse: re-apply duplicated entry, got %d", len(pre))
	}
}

func TestApplyHooks_RefusesToOverwriteInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{ this is not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := applyHooks(path, buildHooksMap("c"), "http://127.0.0.1:7777/api/claude/event")
	if err == nil {
		t.Fatal("want error on invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Errorf("unexpected error: %v", err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse: %v\nfile contents:\n%s", err, b)
	}
	return m
}
