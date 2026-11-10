package core

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
)

// handlerFactory builds a slog handler writing to w at the given level.
type handlerFactory func(w io.Writer, level slog.Level) slog.Handler

var handlerFactories = map[string]handlerFactory{
	"json":   newJSONHandler,
	"text":   newTextHandler,
	"pretty": newTintHandler,
	"auto":   newAutoHandler,
}

func SetupLogger() *slog.Logger {
	level, levelErr := loadLogLevel()
	handler, formatErr := newHandler(os.Stderr, level)

	logger := slog.New(handler)
	slog.SetDefault(logger)

	// Warnings come after setup so they use the configured format.
	if levelErr != nil {
		logger.Warn("invalid LOG_LEVEL, falling back to info",
			slog.String("value", os.Getenv("LOG_LEVEL")),
			slog.String("allowed", "debug, info, warn, error"),
		)
	}
	if formatErr != nil {
		logger.Warn("invalid LOG_FORMAT, falling back to auto",
			slog.String("value", os.Getenv("LOG_FORMAT")),
			slog.String("allowed", "auto, pretty, text, json"),
		)
	}

	return logger
}

// newHandler picks the factory named by LOG_FORMAT (default "auto").
// An unknown name yields the "auto" handler together with an error.
func newHandler(w io.Writer, level slog.Level) (slog.Handler, error) {
	format := strings.ToLower(os.Getenv("LOG_FORMAT"))
	if format == "" {
		format = "auto"
	}

	factory, ok := handlerFactories[format]
	if !ok {
		return newAutoHandler(w, level), fmt.Errorf("invalid LOG_FORMAT %q", format)
	}

	return factory(w, level), nil
}

func newJSONHandler(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
}

func newTextHandler(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
}

// newAutoHandler colors output when w is a terminal and uses plain text otherwise.
func newAutoHandler(w io.Writer, level slog.Level) slog.Handler {
	if isTerminal(w) {
		return newTintHandler(w, level)
	}
	return newTextHandler(w, level)
}

func newTintHandler(w io.Writer, level slog.Level) slog.Handler {
	// no-color.org: any non-empty NO_COLOR disables color.
	return tint.NewHandler(w, &tint.Options{
		Level:      level,
		TimeFormat: time.TimeOnly,
		NoColor:    os.Getenv("NO_COLOR") != "",
	})
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isatty.IsTerminal(f.Fd())
}

// loadLogLevel parses LOG_LEVEL. Unset gives info; an invalid value gives info and an error.
func loadLogLevel() (slog.Level, error) {
	levelStr := os.Getenv("LOG_LEVEL")
	if levelStr == "" {
		return slog.LevelInfo, nil
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(levelStr)); err != nil {
		return slog.LevelInfo, err
	}

	return level, nil
}
