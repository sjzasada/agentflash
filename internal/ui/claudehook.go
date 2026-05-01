package ui

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"agentflash/internal/event"
)

// claudeHookPayload is what Claude Code POSTs from its hook commands.
// We tolerate unknown fields so future hook additions don't break us.
type claudeHookPayload struct {
	HookEventName string                 `json:"hook_event_name"`
	SessionID     string                 `json:"session_id"`
	CWD           string                 `json:"cwd"`
	ToolName      string                 `json:"tool_name"`
	ToolInput     map[string]interface{} `json:"tool_input"`
	ToolUseID     string                 `json:"tool_use_id"`
	Prompt        string                 `json:"prompt"` // UserPromptSubmit
	Source        string                 `json:"source"` // SessionStart
	Message       string                 `json:"message"` // Notification
}

func claudeHookHandler(watchDir string, hub *Hub) http.HandlerFunc {
	rootAbs, _ := filepath.Abs(watchDir)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var p claudeHookPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !cwdInside(p.CWD, rootAbs) {
			// Silently drop events from sessions outside our watch dir.
			w.WriteHeader(http.StatusOK)
			return
		}
		info := buildClaudeInfo(&p)
		if info == nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		hub.broadcast(event.Event{
			TS:     time.Now(),
			Op:     "claude",
			Path:   info.FilePath,
			Claude: info,
		})
		w.WriteHeader(http.StatusOK)
	}
}

func cwdInside(cwd, rootAbs string) bool {
	if cwd == "" {
		return false
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return false
	}
	if abs == rootAbs {
		return true
	}
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != "."
}

func buildClaudeInfo(p *claudeHookPayload) *event.ClaudeInfo {
	phase, ok := mapPhase(p.HookEventName)
	if !ok {
		return nil
	}
	info := &event.ClaudeInfo{
		Phase:     phase,
		SessionID: p.SessionID,
		CWD:       p.CWD,
	}
	switch phase {
	case "user_prompt":
		info.Prompt = truncate(p.Prompt, 240)
		info.Summary = truncate(p.Prompt, 80)
	case "pre", "post":
		info.Tool = p.ToolName
		info.FilePath = stringField(p.ToolInput, "file_path")
		info.Command = stringField(p.ToolInput, "command")
		info.Pattern = stringField(p.ToolInput, "pattern")
		info.SubagentType = stringField(p.ToolInput, "subagent_type")
		if subPrompt := stringField(p.ToolInput, "prompt"); subPrompt != "" {
			info.Prompt = truncate(subPrompt, 240)
		}
		info.Summary = summarizeTool(p.ToolName, p.ToolInput)
	case "session_start":
		info.Summary = "session start"
		if p.Source != "" {
			info.Summary = "session start (" + p.Source + ")"
		}
	case "stop":
		info.Summary = "stop"
	case "subagent_stop":
		info.Summary = "subagent stop"
	case "notification":
		info.Summary = truncate(p.Message, 120)
		if info.Summary == "" {
			info.Summary = "notification"
		}
	}
	return info
}

func mapPhase(hookEvent string) (string, bool) {
	switch hookEvent {
	case "PreToolUse":
		return "pre", true
	case "PostToolUse":
		return "post", true
	case "UserPromptSubmit":
		return "user_prompt", true
	case "SessionStart":
		return "session_start", true
	case "Stop":
		return "stop", true
	case "SubagentStop":
		return "subagent_stop", true
	case "Notification":
		return "notification", true
	}
	return "", false
}

func summarizeTool(tool string, input map[string]interface{}) string {
	switch tool {
	case "Read", "Edit", "Write", "NotebookEdit":
		fp := stringField(input, "file_path")
		if fp == "" {
			return tool
		}
		return tool + " " + filepath.Base(fp)
	case "Bash":
		desc := stringField(input, "description")
		if desc != "" {
			return "Bash " + truncate(desc, 60)
		}
		cmd := stringField(input, "command")
		return "Bash " + truncate(cmd, 60)
	case "Grep", "Glob":
		pat := stringField(input, "pattern")
		if pat == "" {
			return tool
		}
		return tool + " " + truncate(pat, 60)
	case "Task":
		st := stringField(input, "subagent_type")
		if st == "" {
			return "Task"
		}
		return "Task[" + st + "]"
	case "WebFetch":
		url := stringField(input, "url")
		return "WebFetch " + truncate(url, 60)
	case "WebSearch":
		q := stringField(input, "query")
		return "WebSearch " + truncate(q, 60)
	case "":
		return ""
	default:
		return tool
	}
}

func stringField(m map[string]interface{}, k string) string {
	if m == nil {
		return ""
	}
	v, ok := m[k]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
