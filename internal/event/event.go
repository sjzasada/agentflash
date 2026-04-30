package event

import (
	"bufio"
	"encoding/json"
	"io"
	"time"
)

type Event struct {
	TS      time.Time `json:"ts"`
	Op      string    `json:"op"`
	Path    string    `json:"path"`
	Process string    `json:"process"`
	PID     int       `json:"pid"`
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
