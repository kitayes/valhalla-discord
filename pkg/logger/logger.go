package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

type Logger struct {
	logger *slog.Logger
}

func NewLogger(cfg *Config) *Logger {
	opts := &slog.HandlerOptions{
		Level: getLoggerLevel(cfg.Level),
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, opts))
	return &Logger{
		logger: logger,
	}
}

func (l *Logger) Error(format string, v ...interface{}) {
	l.log(slog.LevelError, format, v...)
}
func (l *Logger) Warn(format string, v ...interface{}) {
	l.log(slog.LevelWarn, format, v...)
}
func (l *Logger) Info(format string, v ...interface{}) {
	l.log(slog.LevelInfo, format, v...)
}
func (l *Logger) Debug(format string, v ...interface{}) {
	l.log(slog.LevelDebug, format, v...)
}

// log renders the Printf-style format string into the slog message.
// slog treats variadic arguments as key/value pairs, so passing them through
// untouched would emit the raw format string plus a !BADKEY entry.
func (l *Logger) log(level slog.Level, format string, v ...interface{}) {
	if !l.logger.Enabled(context.Background(), level) {
		return
	}
	msg := format
	if len(v) > 0 {
		msg = fmt.Sprintf(format, v...)
	}
	l.logger.Log(context.Background(), level, msg)
}

func getLoggerLevel(logLevel string) slog.Level {
	switch strings.ToLower(logLevel) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelDebug
	}
}
