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
	"net/http"
	"time"

	"github.com/baowk/dilu-go-kit/log"
)

// Notifier sends events to a target service via HTTP POST.
type Notifier struct {
	baseURL string
	client  *http.Client
}

var global *Notifier

// Init initializes the global notifier. wsBaseURL is the target's internal API
// base URL, e.g. "http://mf-ws:9020".
func Init(wsBaseURL string) {
	if wsBaseURL == "" {
		return
	}
	global = &Notifier{
		baseURL: wsBaseURL,
		client:  &http.Client{Timeout: 3 * time.Second},
	}
	log.Info("notifier initialized", "target", wsBaseURL)
}

// Send posts an event to /internal/notify/{resource}.
// payload is JSON-serialized and sent as the request body.
func Send(resource string, payload any) {
	if global == nil {
		return
	}
	global.send(resource, payload)
}

// SendContext is like Send but accepts a context for traceId propagation.
func SendContext(ctx context.Context, resource string, payload any) {
	if global == nil {
		return
	}
	global.sendCtx(ctx, resource, payload)
}

func (n *Notifier) send(resource string, payload any) {
	n.sendCtx(context.Background(), resource, payload)
}

func (n *Notifier) sendCtx(ctx context.Context, resource string, payload any) {
	url := fmt.Sprintf("%s/internal/notify/%s", n.baseURL, resource)
	body, err := json.Marshal(payload)
	if err != nil {
		log.Error("notify marshal failed", "resource", resource, "error", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Error("notify request failed", "resource", resource, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// Propagate traceId
	if traceID := log.GetTraceID(ctx); traceID != "" {
		req.Header.Set("X-Trace-Id", traceID)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		log.Warn("notify send failed", "resource", resource, "url", url, "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Warn("notify bad response", "resource", resource, "status", resp.StatusCode)
	}
}
