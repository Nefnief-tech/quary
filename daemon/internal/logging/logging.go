// Package logging builds the daemon's slog logger (JSON, debug in dev).
package logging

import (
	"log/slog"
	"os"
)

func New(dev bool) *slog.Logger {
	level := slog.LevelInfo
	if dev {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
