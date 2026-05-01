package ui

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestClaudeHook_FilterByCWD(t *testing.T) {
	hub := NewHub(100, false)
	h := claudeHookHandler("/Users/stef/dev/agentflash", hub)

	t.Run("inside watch dir is broadcast", func(t *testing.T) {
		body, _ := json.Marshal(map[string]interface{}{
			"hook_event_name": "PreToolUse",
			"session_id":      "s1",
			"cwd":             "/Users/stef/dev/agentflash",
			"tool_name":       "Edit",
			"tool_input":      map[string]interface{}{"file_path": "/Users/stef/dev/agentflash/main.go"},
		})
		req := httptest.NewRequest("POST", "/api/claude/event", bytes.NewReader(body))
		rr := httptest.NewRecorder()
		h(rr, req)
		if rr.Code != 200 {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		if hub.bcastCount.Load() != 1 {
			t.Fatalf("want 1 broadcast, got %d", hub.bcastCount.Load())
		}
	})

	t.Run("outside watch dir is dropped", func(t *testing.T) {
		before := hub.bcastCount.Load()
		body, _ := json.Marshal(map[string]interface{}{
			"hook_event_name": "PreToolUse",
			"cwd":             "/Users/other/project",
			"tool_name":       "Edit",
		})
		req := httptest.NewRequest("POST", "/api/claude/event", bytes.NewReader(body))
		rr := httptest.NewRecorder()
		h(rr, req)
		if rr.Code != 200 {
			t.Fatalf("want 200, got %d", rr.Code)
		}
		if hub.bcastCount.Load() != before {
			t.Fatalf("expected no broadcast, got delta %d", hub.bcastCount.Load()-before)
		}
	})
}

func TestSummarizeTool(t *testing.T) {
	cases := []struct {
		tool  string
		input map[string]interface{}
		want  string
	}{
		{"Read", map[string]interface{}{"file_path": "/a/b/main.go"}, "Read main.go"},
		{"Edit", map[string]interface{}{"file_path": "/a/b/style.css"}, "Edit style.css"},
		{"Bash", map[string]interface{}{"description": "run tests"}, "Bash run tests"},
		{"Bash", map[string]interface{}{"command": "go test ./..."}, "Bash go test ./..."},
		{"Grep", map[string]interface{}{"pattern": "WebSocket"}, "Grep WebSocket"},
		{"Task", map[string]interface{}{"subagent_type": "Explore"}, "Task[Explore]"},
		{"WebSearch", map[string]interface{}{"query": "fs_usage hooks"}, "WebSearch fs_usage hooks"},
		{"Custom", nil, "Custom"},
	}
	for _, c := range cases {
		got := summarizeTool(c.tool, c.input)
		if got != c.want {
			t.Errorf("summarizeTool(%q, %v) = %q, want %q", c.tool, c.input, got, c.want)
		}
	}
}

func TestCWDInside(t *testing.T) {
	root := "/Users/stef/dev/agentflash"
	yes := []string{
		"/Users/stef/dev/agentflash",
		"/Users/stef/dev/agentflash/internal",
		"/Users/stef/dev/agentflash/internal/ui",
	}
	no := []string{
		"",
		"/Users/stef/dev",
		"/Users/stef/dev/agentflash-other",
		"/tmp",
	}
	for _, d := range yes {
		if !cwdInside(d, root) {
			t.Errorf("cwdInside(%q) want true", d)
		}
	}
	for _, d := range no {
		if cwdInside(d, root) {
			t.Errorf("cwdInside(%q) want false", d)
		}
	}
}
