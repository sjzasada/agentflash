package tap

import "log"

type debugLogger struct {
	on bool
	l  *log.Logger
}

func (d *debugLogger) Printf(format string, args ...any) {
	if d == nil || !d.on {
		return
	}
	d.l.Printf(format, args...)
}
