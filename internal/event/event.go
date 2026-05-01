package event

import (
	"bufio"
	"encoding/json"
	"io"
	"time"
)

type Event struct {
	TS      time.Time   `json:"ts"`
	Op      string      `json:"op"`
	Path    string      `json:"path,omitempty"`
	Process string      `json:"process,omitempty"`
	PID     int         `json:"pid,omitempty"`
	Claude  *ClaudeInfo `json:"claude,omitempty"`
}

// ClaudeInfo is the structured payload for op="claude" events. Only
// the Phase field is always set; the rest are populated based on the
// hook event type and tool name.
type ClaudeInfo struct {
	Phase        string `json:"phase"` // pre|post|user_prompt|session_start|stop|notification|subagent_stop
	Tool         string `json:"tool,omitempty"`
	Summary      string `json:"summary,omitempty"`
	FilePath     string `json:"filePath,omitempty"`
	Command      string `json:"command,omitempty"`
	Pattern      string `json:"pattern,omitempty"`
	SubagentType string `json:"subagentType,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	SessionID    string `json:"sessionId,omitempty"`
	CWD          string `json:"cwd,omitempty"`
}

type Writer struct {
	enc *json.Encoder
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{enc: json.NewEncoder(w)}
}

func (w *Writer) Write(e Event) error {
	return w.enc.Encode(e)
}

type Reader struct {
	sc *bufio.Scanner
}

func NewReader(r io.Reader) *Reader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	return &Reader{sc: sc}
}

func (r *Reader) Next() (Event, bool, error) {
	if !r.sc.Scan() {
		if err := r.sc.Err(); err != nil {
			return Event{}, false, err
		}
		return Event{}, false, nil
	}
	var e Event
	if err := json.Unmarshal(r.sc.Bytes(), &e); err != nil {
		return Event{}, false, err
	}
	return e, true, nil
}
