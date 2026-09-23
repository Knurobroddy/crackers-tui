// Package logx holds logging helpers shared by several packages.
package logx

import "log/slog"

// OrDiscard returns l, or a logger that discards everything if l is nil.
func OrDiscard(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.New(slog.DiscardHandler)
	}
	return l
}
