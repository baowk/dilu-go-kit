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

var global Logger = newSlogLogger("debug", "", "", nil)

// FileConfig enables file logging with rotation via lumberjack.
// Leave zero-value (or nil pointer) to disable file output.
type FileConfig struct {
	Path       string `mapstructure:"path"`       // log file path, e.g. "logs/app.log"
	MaxSize    int    `mapstructure:"maxSize"`     // max size in MB before rotation (default 100)
	MaxAge     int    `mapstructure:"maxAge"`      // max days to retain old files (default 7)
	MaxBackups int    `mapstructure:"maxBackups"`  // max number of old files (default 5)
	Compress   bool   `mapstructure:"compress"`    // gzip rotated files (default false)
}

func (f *FileConfig) maxSize() int {
	if f.MaxSize > 0 {
		return f.MaxSize
	}
	return 100
}

func (f *FileConfig) maxAge() int {
	if f.MaxAge > 0 {
		return f.MaxAge
	}
	return 7
}

func (f *FileConfig) maxBackups() int {
	if f.MaxBackups > 0 {
		return f.MaxBackups
	}
	return 5
}

// Init initializes the global logger. Call once at startup.
//
//	log.Init("debug", "mf-user", "", nil)                                       // console only (default)
//	log.Init("release", "mf-user", "file", &log.FileConfig{Path: "logs/app.log"}) // file only
//	log.Init("release", "mf-user", "both", &log.FileConfig{Path: "logs/app.log"}) // console + file
//
// output: "console" (default), "file", "both"
func Init(mode, serviceName, output string, file *FileConfig) {
	global = newSlogLogger(mode, serviceName, output, file)
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
