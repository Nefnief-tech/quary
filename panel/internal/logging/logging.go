// Package logging builds the panel's slog logger. JSON output; debug level
// in dev. Request logging lives in the api middleware (with secret
// redaction); nothing here may ever receive tokens or credentials.
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
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
}
