// Package logging configures structured (log/slog) logging.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// LevelBlock is above ERROR so block events are never filtered out.
const LevelBlock = slog.Level(12)

// New returns a logger writing to w in "json" or "text" format.
func New(w io.Writer, level, format string) (*slog.Logger, error) {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "", "info":
		lv = slog.LevelInfo
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown log level %q", level)
	}
	opts := &slog.HandlerOptions{
		Level: lv,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				if l, ok := a.Value.Any().(slog.Level); ok && l == LevelBlock {
					a.Value = slog.StringValue("BLOCK")
				}
			}
			return a
		},
	}
	switch strings.ToLower(format) {
	case "", "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	}
	return nil, fmt.Errorf("unknown log format %q", format)
}
