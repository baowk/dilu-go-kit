package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNotifierSendsAuthenticatedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/notify/task" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	n, err := New(Config{BaseURL: server.URL, Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.SendContext(context.Background(), "task", map[string]any{"id": 1}); err != nil {
		t.Fatal(err)
	}
}

func TestNotifierRejectsPathInjection(t *testing.T) {
	n, err := New(Config{BaseURL: "http://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.SendContext(context.Background(), "../admin", nil); err == nil {
		t.Fatal("expected invalid resource error")
	}
}

func TestInitEmptyClearsGlobalNotifier(t *testing.T) {
	if err := InitConfig(Config{BaseURL: "http://example.com"}); err != nil {
		t.Fatal(err)
	}
	Init("")
	if err := SendContextE(context.Background(), "task", nil); err == nil {
		t.Fatal("expected not initialized error")
	}
}

func TestNotifierRequireTLS(t *testing.T) {
	if _, err := New(Config{BaseURL: "http://example.com", RequireTLS: true}); err == nil {
		t.Fatal("expected TLS requirement to reject HTTP endpoint")
	}
}

func TestNotifierRejectsOversizedPayload(t *testing.T) {
	n, err := New(Config{BaseURL: "http://example.com", MaxPayloadBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.SendContext(context.Background(), "task", map[string]any{"payload": "too-large"}); err == nil {
		t.Fatal("expected oversized payload error")
	}
}
