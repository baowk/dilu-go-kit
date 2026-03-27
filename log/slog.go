package log

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"

	"gopkg.in/lumberjack.v2"
)

// slogLogger wraps slog.Logger to implement our Logger interface.
type slogLogger struct {
	l *slog.Logger
}

func newSlogLogger(mode, serviceName, output string, file *FileConfig) *slogLogger {
	var handler slog.Handler

	// Determine output writer based on output mode: "console" (default), "file", "both"
	var w io.Writer
	switch output {
	case "file":
		if file == nil || file.Path == "" {
			w = os.Stdout // fallback if no file path configured
		} else {
			w = &lumberjack.Logger{
				Filename:   file.Path,
				MaxSize:    file.maxSize(),
				MaxAge:     file.maxAge(),
				MaxBackups: file.maxBackups(),
				Compress:   file.Compress,
			}
		}
	case "both":
		if file != nil && file.Path != "" {
			lj := &lumberjack.Logger{
				Filename:   file.Path,
				MaxSize:    file.maxSize(),
				MaxAge:     file.maxAge(),
				MaxBackups: file.maxBackups(),
				Compress:   file.Compress,
			}
			w = io.MultiWriter(os.Stdout, lj)
		} else {
			w = os.Stdout // fallback if no file path configured
		}
	default: // "console" or empty
		w = os.Stdout
	}

	if mode == "release" || mode == "production" {
		// JSON output for production (structured, machine-parseable)
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})
	} else {
		// Text output for development (human-readable)
		handler = slog.NewTextHandler(w, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})
	}

	// Wrap with traceId handler
	handler = &traceHandler{inner: handler}

	l := slog.New(handler)
	if serviceName != "" {
		l = l.With("service", serviceName)
	}

	// Also set as slog default so third-party libs using slog.Info() get trace support
	slog.SetDefault(l)

	return &slogLogger{l: l}
}

func (s *slogLogger) Debug(msg string, args ...any) { s.l.Debug(msg, args...) }
func (s *slogLogger) Info(msg string, args ...any)  { s.l.Info(msg, args...) }
func (s *slogLogger) Warn(msg string, args ...any)  { s.l.Warn(msg, args...) }
func (s *slogLogger) Error(msg string, args ...any) { s.l.Error(msg, args...) }

func (s *slogLogger) DebugContext(ctx context.Context, msg string, args ...any) {
	s.l.DebugContext(ctx, msg, args...)
}
func (s *slogLogger) InfoContext(ctx context.Context, msg string, args ...any) {
	s.l.InfoContext(ctx, msg, args...)
}
func (s *slogLogger) WarnContext(ctx context.Context, msg string, args ...any) {
	s.l.WarnContext(ctx, msg, args...)
}
func (s *slogLogger) ErrorContext(ctx context.Context, msg string, args ...any) {
	s.l.ErrorContext(ctx, msg, args...)
}

func (s *slogLogger) With(args ...any) Logger {
	return &slogLogger{l: s.l.With(args...)}
}

// ── traceHandler: injects trace_id from context into every log record ──

type traceHandler struct {
	inner slog.Handler
}

func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if traceID := GetTraceID(ctx); traceID != "" {
		r.AddAttrs(slog.String("trace_id", traceID))
	}
	// Ensure timestamp
	if r.Time.IsZero() {
		r.Time = time.Now()
	}
	return h.inner.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{inner: h.inner.WithGroup(name)}
}
