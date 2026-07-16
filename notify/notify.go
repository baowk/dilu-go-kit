// Package notify provides a simple HTTP-based event notifier.
// Used by backend services to push real-time events to a WebSocket gateway.
//
// Usage:
//
//	notify.Init("http://mf-ws:9020")
//	notify.Send("env", map[string]any{"action":"created","env_id":123,"workspace_id":1})
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/baowk/dilu-go-kit/log"
)

// Notifier sends events to a target service via HTTP POST.
type Notifier struct {
	baseURL string
	client  *http.Client
	token   string
}

var global atomic.Pointer[Notifier]

// Config configures a Notifier.
type Config struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

// New creates an instance-scoped notifier.
func New(cfg Config) (*Notifier, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("notify: empty base URL")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("notify: invalid base URL %q", cfg.BaseURL)
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	return &Notifier{baseURL: baseURL, client: client, token: cfg.Token}, nil
}

// Init initializes the global notifier. wsBaseURL is the target's internal API
// base URL, e.g. "http://mf-ws:9020".
func Init(wsBaseURL string) {
	if wsBaseURL == "" {
		global.Store(nil)
		return
	}
	n, err := New(Config{BaseURL: wsBaseURL})
	if err != nil {
		log.Error("notifier init failed", "error", err)
		global.Store(nil)
		return
	}
	global.Store(n)
	log.Info("notifier initialized", "target", wsBaseURL)
}

// InitConfig initializes the global notifier with authentication or a custom client.
func InitConfig(cfg Config) error {
	if cfg.BaseURL == "" {
		global.Store(nil)
		return nil
	}
	n, err := New(cfg)
	if err != nil {
		return err
	}
	global.Store(n)
	return nil
}

// Send posts an event to /internal/notify/{resource}.
// payload is JSON-serialized and sent as the request body.
func Send(resource string, payload any) {
	n := global.Load()
	if n == nil {
		return
	}
	if err := n.SendContext(context.Background(), resource, payload); err != nil {
		log.Warn("notify send failed", "resource", resource, "error", err)
	}
}

// SendContext is like Send but accepts a context for traceId propagation.
func SendContext(ctx context.Context, resource string, payload any) {
	n := global.Load()
	if n == nil {
		return
	}
	if err := n.SendContext(ctx, resource, payload); err != nil {
		log.Warn("notify send failed", "resource", resource, "error", err)
	}
}

// SendContextE sends through the global notifier and returns delivery errors.
func SendContextE(ctx context.Context, resource string, payload any) error {
	n := global.Load()
	if n == nil {
		return fmt.Errorf("notify: not initialized")
	}
	return n.SendContext(ctx, resource, payload)
}

// SendContext sends an event and reports transport or non-2xx failures.
func (n *Notifier) SendContext(ctx context.Context, resource string, payload any) error {
	if n == nil {
		return fmt.Errorf("notify: notifier is nil")
	}
	if !validResource(resource) {
		return fmt.Errorf("notify: invalid resource %q", resource)
	}
	target, err := url.JoinPath(n.baseURL, "internal", "notify", resource)
	if err != nil {
		return fmt.Errorf("notify: build URL: %w", err)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if n.token != "" {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}

	// Propagate traceId
	if traceID := log.GetTraceID(ctx); traceID != "" {
		req.Header.Set("X-Trace-Id", traceID)
		req.Header.Set("X-Request-Id", traceID)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: send: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notify: unexpected HTTP status %d", resp.StatusCode)
	}
	return nil
}

func validResource(resource string) bool {
	if resource == "" || len(resource) > 100 {
		return false
	}
	for _, r := range resource {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}
