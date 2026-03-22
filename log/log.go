// Package log provides a unified logging interface with traceId support.
//
// All services should use this package instead of slog/zerolog directly.
// The underlying implementation can be swapped without changing call sites.
//
// Usage:
//
//	log.Info("server started", "port", 8080)
//	log.InfoContext(ctx, "created env", "env_id", 123)  // auto injects trace_id
//	log.With("service", "mf-user").Info("ready")
package log

import "context"

// Logger is the unified logging interface. All services depend on this,
// never on a specific logging library directly.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)

	// Context-aware: automatically extracts trace_id from ctx
	DebugContext(ctx context.Context, msg string, args ...any)
	InfoContext(ctx context.Context, msg string, args ...any)
	WarnContext(ctx context.Context, msg string, args ...any)
	ErrorContext(ctx context.Context, msg string, args ...any)

	// With returns a child logger with additional key-value pairs
	With(args ...any) Logger
}

var global Logger = newSlogLogger("debug", "")

// Init initializes the global logger. Call once at startup.
//
//	log.Init("debug", "mf-user")  // mode: "debug" (text) or "release" (json)
func Init(mode, serviceName string) {
	global = newSlogLogger(mode, serviceName)
}

// SetLogger replaces the global logger with a custom implementation.
func SetLogger(l Logger) { global = l }

// L returns the global logger instance.
func L() Logger { return global }

// ── Package-level convenience functions ──

func Debug(msg string, args ...any)   { global.Debug(msg, args...) }
func Info(msg string, args ...any)    { global.Info(msg, args...) }
func Warn(msg string, args ...any)    { global.Warn(msg, args...) }
func Error(msg string, args ...any)   { global.Error(msg, args...) }

func DebugContext(ctx context.Context, msg string, args ...any) { global.DebugContext(ctx, msg, args...) }
func InfoContext(ctx context.Context, msg string, args ...any)  { global.InfoContext(ctx, msg, args...) }
func WarnContext(ctx context.Context, msg string, args ...any)  { global.WarnContext(ctx, msg, args...) }
func ErrorContext(ctx context.Context, msg string, args ...any) { global.ErrorContext(ctx, msg, args...) }

func With(args ...any) Logger { return global.With(args...) }
