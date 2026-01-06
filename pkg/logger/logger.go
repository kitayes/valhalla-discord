package logger

import (
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
	l.logger.Error(format, v...)
}
func (l *Logger) Warn(format string, v ...interface{}) {
	l.logger.Warn(format, v...)
}
func (l *Logger) Info(format string, v ...interface{}) {
	l.logger.Info(format, v...)
}
func (l *Logger) Debug(format string, v ...interface{}) {
	l.logger.Debug(format, v...)
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
