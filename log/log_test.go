package log

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
)

type noopLogger struct{}

type testCloser struct{}

func (*testCloser) Close() error { return nil }

func (noopLogger) Debug(string, ...any)                         {}
func (noopLogger) Info(string, ...any)                          {}
func (noopLogger) Warn(string, ...any)                          {}
func (noopLogger) Error(string, ...any)                         {}
func (noopLogger) DebugContext(context.Context, string, ...any) {}
func (noopLogger) InfoContext(context.Context, string, ...any)  {}
func (noopLogger) WarnContext(context.Context, string, ...any)  {}
func (noopLogger) ErrorContext(context.Context, string, ...any) {}
func (noopLogger) With(...any) Logger                           { return noopLogger{} }

func TestSetLoggerRejectsNil(t *testing.T) {
	if err := SetLogger(nil); err == nil {
		t.Fatal("expected nil logger error")
	}
}

func TestConcurrentLoggingAndLoggerSwap(t *testing.T) {
	original := L()
	defer SetLogger(original)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = SetLogger(noopLogger{})
			Info("test")
		}()
	}
	wg.Wait()
}

func TestSlogLoggerWithPreservesCloser(t *testing.T) {
	closer := &testCloser{}
	parent := &slogLogger{l: slog.New(slog.NewTextHandler(io.Discard, nil)), closer: closer}
	child, ok := parent.With("key", "value").(*slogLogger)
	if !ok || child.closer != closer {
		t.Fatal("derived logger did not preserve the output closer")
	}
}
