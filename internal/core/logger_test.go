// ABOUTME: Tests log level parsing and LOG_FORMAT handler selection.
// ABOUTME: Handlers write to an in-memory buffer so output can be asserted.
package core

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

const ansiEscape = "\x1b["

func TestLoadLogLevel(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		want    slog.Level
		wantErr bool
	}{
		{"unset defaults to info", "", slog.LevelInfo, false},
		{"debug", "debug", slog.LevelDebug, false},
		{"case insensitive", "WARN", slog.LevelWarn, false},
		{"error", "error", slog.LevelError, false},
		{"offset", "info+2", slog.LevelInfo + 2, false},
		{"invalid falls back to info", "OOO", slog.LevelInfo, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tt.env)

			got, err := loadLogLevel()

			if got != tt.want {
				t.Errorf("level = %v, want %v", got, tt.want)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func logOnce(t *testing.T, format, noColor string, level slog.Level) (string, error) {
	t.Helper()
	t.Setenv("LOG_FORMAT", format)
	t.Setenv("NO_COLOR", noColor)

	var buf bytes.Buffer
	handler, err := newHandler(&buf, level)
	slog.New(handler).Info("hello", slog.String("key", "value"))

	return buf.String(), err
}

func TestNewHandlerFormats(t *testing.T) {
	t.Run("json emits valid JSON", func(t *testing.T) {
		out, err := logOnce(t, "json", "", slog.LevelInfo)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var entry map[string]any
		if jsonErr := json.Unmarshal([]byte(out), &entry); jsonErr != nil {
			t.Fatalf("output is not JSON: %v\n%s", jsonErr, out)
		}
		if entry["msg"] != "hello" || entry["key"] != "value" {
			t.Errorf("unexpected entry: %v", entry)
		}
	})

	t.Run("text emits key=value pairs", func(t *testing.T) {
		out, err := logOnce(t, "text", "", slog.LevelInfo)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, "level=INFO") || !strings.Contains(out, "key=value") {
			t.Errorf("unexpected text output: %s", out)
		}
	})

	t.Run("format name is case insensitive", func(t *testing.T) {
		out, err := logOnce(t, "JSON", "", slog.LevelInfo)

		if err != nil || !strings.HasPrefix(out, "{") {
			t.Errorf("err = %v, output = %s", err, out)
		}
	})

	t.Run("pretty colors output", func(t *testing.T) {
		out, err := logOnce(t, "pretty", "", slog.LevelInfo)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, ansiEscape) {
			t.Errorf("expected ANSI colors, got %q", out)
		}
	})

	t.Run("pretty honors NO_COLOR", func(t *testing.T) {
		out, err := logOnce(t, "pretty", "1", slog.LevelInfo)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(out, ansiEscape) {
			t.Errorf("expected no ANSI colors, got %q", out)
		}
	})

	t.Run("pretty ignores empty NO_COLOR", func(t *testing.T) {
		out, _ := logOnce(t, "pretty", "", slog.LevelInfo)

		if !strings.Contains(out, ansiEscape) {
			t.Errorf("expected ANSI colors, got %q", out)
		}
	})
}

func TestNewHandlerAuto(t *testing.T) {
	// A buffer is never a terminal, so auto must pick plain text.
	for _, format := range []string{"", "auto"} {
		t.Run("format="+format, func(t *testing.T) {
			out, err := logOnce(t, format, "", slog.LevelInfo)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(out, "level=INFO") || strings.Contains(out, ansiEscape) {
				t.Errorf("expected plain text, got %q", out)
			}
		})
	}
}

func TestNewHandlerInvalidFormat(t *testing.T) {
	out, err := logOnce(t, "xml", "", slog.LevelInfo)

	if err == nil {
		t.Fatal("expected an error for an unknown format")
	}
	if !strings.Contains(out, "level=INFO") {
		t.Errorf("expected fallback to auto (plain text here), got %q", out)
	}
}

func TestNewHandlerRespectsLevel(t *testing.T) {
	t.Setenv("LOG_FORMAT", "text")

	var buf bytes.Buffer
	handler, _ := newHandler(&buf, slog.LevelWarn)
	logger := slog.New(handler)

	logger.Info("dropped")
	logger.Warn("kept")

	out := buf.String()
	if strings.Contains(out, "dropped") || !strings.Contains(out, "kept") {
		t.Errorf("level filtering failed: %s", out)
	}
}
