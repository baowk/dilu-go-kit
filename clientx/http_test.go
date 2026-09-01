package clientx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPClientRetriesTransientStatusForIdempotentMethod(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := NewHTTPClient(HTTPClientConfig{Retry: RetryConfig{InitialBackoff: 1, MaxBackoff: 1, DisableJitter: true}})
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if calls.Load() != 3 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestHTTPClientDoesNotRetryPostByDefault(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := NewHTTPClient(HTTPClientConfig{Retry: RetryConfig{MaxAttempts: 3, InitialBackoff: 1, MaxBackoff: 1, DisableJitter: true}})
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("payload"))
	_, err := c.Do(t.Context(), req)
	if err == nil || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}
