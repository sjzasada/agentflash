package ui

import "log"

// debugLogger is a zero-cost wrapper that emits to the standard logger
// only when debug mode is enabled. Construct with newDebugLogger.
type debugLogger struct {
	on bool
}

func newDebugLogger(on bool) *debugLogger {
	return &debugLogger{on: on}
}

func (d *debugLogger) Printf(format string, args ...any) {
	if d == nil || !d.on {
		return
	}
	log.Printf(format, args...)
}
