package config

import (
	"log/slog"
	"strings"
)

// LevelTrace is the most verbose log level, one step below debug. slog has
// no built-in trace level; -8 sits below LevelDebug (-4). slog's String()
// renders it "DEBUG-4", so consumers print TRACE explicitly. This package
// hosts the definition because LOG_LEVEL validation lives here and config
// is a bottom-layer package that must not import telemetry; telemetry keeps
// a forwarding alias for its existing users.
const LevelTrace = slog.Level(-8)

// ParseLevel parses a LOG_LEVEL-style string into a slog level. The empty
// string returns ok=false (caller falls back to its default). "trace"
// (case-insensitive) maps to LevelTrace; the four slog names are accepted
// as before. Canonical home: telemetry.ParseLevel forwards here so logger
// construction and LOG_LEVEL validation share one mapping.
func ParseLevel(s string) (slog.Level, bool) {
	if s == "" {
		return 0, false
	}
	if strings.EqualFold(s, "trace") {
		return LevelTrace, true
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return 0, false
	}
	return level, true
}
