package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

func New(level string) (*slog.Logger, error) {
	return NewWithWriter(level, os.Stderr)
}

func NewWithWriter(level string, writer io.Writer) (*slog.Logger, error) {
	slogLevel, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	handler := slog.NewTextHandler(writer, &slog.HandlerOptions{
		Level: slogLevel,
	})
	return slog.New(handler), nil
}

func ParseLevel(level string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unsupported log level %q", level)
	}
}
