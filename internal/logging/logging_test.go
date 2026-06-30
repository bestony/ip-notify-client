package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  slog.Level
	}{
		{name: "debug", input: " debug ", want: slog.LevelDebug},
		{name: "info", input: "", want: slog.LevelInfo},
		{name: "warn", input: "warning", want: slog.LevelWarn},
		{name: "error", input: "ERROR", want: slog.LevelError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseLevel(test.input)
			if err != nil {
				t.Fatalf("parse level: %v", err)
			}
			if got != test.want {
				t.Fatalf("expected %s, got %s", test.want, got)
			}
		})
	}
}

func TestParseLevelRejectsUnsupportedValue(t *testing.T) {
	level, err := ParseLevel("trace")
	if err == nil {
		t.Fatal("expected unsupported level error")
	}
	if level != slog.LevelInfo {
		t.Fatalf("expected info fallback level, got %s", level)
	}
}

func TestNewWithWriterHonorsLevel(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewWithWriter("warn", &output)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	logger.Info("hidden")
	logger.Warn("visible")
	if strings.Contains(output.String(), "hidden") {
		t.Fatalf("info log should be filtered at warn level: %s", output.String())
	}
	if !strings.Contains(output.String(), "visible") {
		t.Fatalf("warn log should be written: %s", output.String())
	}
}

func TestNewWithWriterReturnsParseError(t *testing.T) {
	if _, err := NewWithWriter("trace", &bytes.Buffer{}); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestNew(t *testing.T) {
	logger, err := New("info")
	if err != nil {
		t.Fatalf("new default logger: %v", err)
	}
	if logger == nil {
		t.Fatal("expected logger")
	}
}
