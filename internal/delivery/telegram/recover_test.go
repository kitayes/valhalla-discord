package telegram

import (
	"strings"
	"testing"
)

type captureLogger struct{ errors []string }

func (l *captureLogger) Info(string, ...interface{})  {}
func (l *captureLogger) Debug(string, ...interface{}) {}
func (l *captureLogger) Warn(string, ...interface{})  {}
func (l *captureLogger) Error(msg string, args ...interface{}) {
	l.errors = append(l.errors, msg)
}

// The Telegram update loop runs every handler on the polling goroutine with no
// recover, unlike the Discord bot: one nil dereference in a handler killed the
// whole process, Discord side included.
func TestSafelyRecoversHandlerPanic(t *testing.T) {
	log := &captureLogger{}
	b := &Bot{logger: log}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic escaped safely(): %v", r)
			}
		}()
		b.safely("test handler", func() {
			var p *int
			_ = *p
		})
	}()

	if len(log.errors) != 1 || !strings.Contains(log.errors[0], "PANIC") {
		t.Errorf("expected one PANIC log entry, got %v", log.errors)
	}
}
