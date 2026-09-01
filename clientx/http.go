package clientx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/baowk/dilu-go-kit/metrics"
	"github.com/baowk/dilu-go-kit/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// HTTPClientConfig configures the shared outbound HTTP client.
type HTTPClientConfig struct {
	Client             *http.Client
	Timeout            time.Duration
	Retry              RetryConfig
	Breaker            *Breaker
	Telemetry          *telemetry.Provider
	ServiceName        string
	RetryNonIdempotent bool
	MaxBodyBytes       int64
}

// HTTPClient applies timeout, retry, circuit breaking and trace propagation
// consistently to outbound HTTP calls.
type HTTPClient struct {
	client             *http.Client
	timeout            time.Duration
	retry              RetryConfig
	breaker            *Breaker
	telemetry          *telemetry.Provider
	serviceName        string
	retryNonIdempotent bool
	maxBodyBytes       int64
}

// NewHTTPClient creates a governed HTTP client. The default retry policy only
// retries idempotent methods and transient transport/status failures.
func NewHTTPClient(cfg HTTPClientConfig) *HTTPClient {
	client := cfg.Client
	if client == nil {
		client = &http.Client{}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	retry := cfg.Retry
	if retry.Retryable == nil {
		retry.Retryable = func(err error) bool {
			var statusErr *HTTPStatusError
			if errors.As(err, &statusErr) {
				return statusErr.StatusCode == http.StatusTooManyRequests || statusErr.StatusCode >= 500
			}
			if errors.Is(err, context.Canceled) {
				return false
			}
			var netErr net.Error
			return errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded)
		}
	}
	retry = normalizeRetryConfig(retry)
	if retry.Budget <= 0 {
		retry.Budget = timeout * time.Duration(retry.MaxAttempts)
	}
	maxBodyBytes := cfg.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = 10 << 20
	}
	if maxBodyBytes > 256<<20 {
		maxBodyBytes = 256 << 20
	}
	return &HTTPClient{client: client, timeout: timeout, retry: retry, breaker: cfg.Breaker,
		telemetry: cfg.Telemetry, serviceName: strings.TrimSpace(cfg.ServiceName), retryNonIdempotent: cfg.RetryNonIdempotent, maxBodyBytes: maxBodyBytes}
}

// HTTPStatusError reports a non-success HTTP response after request execution.
type HTTPStatusError struct {
	StatusCode int
	Status     string
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.Status != "" {
		return fmt.Sprintf("http status %d (%s)", e.StatusCode, e.Status)
	}
	return fmt.Sprintf("http status %d", e.StatusCode)
}

// Do executes req and returns a successful response. The caller owns and must
// close the response body. Non-2xx responses return HTTPStatusError.
func (c *HTTPClient) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("clientx: HTTP client is nil")
	}
	if req == nil {
		return nil, errors.New("clientx: HTTP request is nil")
	}
	maxBodyBytes := c.maxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = 10 << 20
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var body []byte
	// Buffer only when retries may replay the request. Non-idempotent calls
	// keep streaming the original body, avoiding an unbounded memory copy.
	bufferBody := req.Body != nil && c.retry.MaxAttempts > 1 && (c.retryNonIdempotent || isIdempotent(req.Method))
	if bufferBody {
		var err error
		body, err = io.ReadAll(io.LimitReader(req.Body, maxBodyBytes+1))
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(body)) > maxBodyBytes {
			return nil, ErrRequestBodyTooLarge
		}
	}
	if c.breaker != nil {
		// Execute through the breaker while retaining the final response.
		var result *http.Response
		err := c.breaker.Do(ctx, func(callCtx context.Context) error {
			return c.doWithResponse(callCtx, req, body, &result)
		})
		return result, err
	}
	var result *http.Response
	err := c.doWithResponse(ctx, req, body, &result)
	return result, err
}

var ErrRequestBodyTooLarge = errors.New("clientx: request body exceeds configured limit")

func (c *HTTPClient) doWithResponse(ctx context.Context, req *http.Request, body []byte, result **http.Response) error {
	if !c.retryNonIdempotent && !isIdempotent(req.Method) {
		return c.single(ctx, req, body, result)
	}
	return Do(ctx, c.retry, func(callCtx context.Context) error { return c.single(callCtx, req, body, result) })
}

func (c *HTTPClient) single(ctx context.Context, req *http.Request, body []byte, result **http.Response) error {
	start := time.Now()
	statusLabel := "error"
	defer func() { metrics.ObserveDependency(c.serviceName, req.Method, statusLabel, time.Since(start)) }()
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	attemptReq := req.Clone(callCtx)
	if body != nil {
		attemptReq.Body = io.NopCloser(bytes.NewReader(body))
		attemptReq.ContentLength = int64(len(body))
	}
	var span trace.Span
	if c.telemetry != nil {
		tracer := c.telemetry.Tracer("github.com/baowk/dilu-go-kit/clientx/http")
		spanCtx, started := tracer.Start(attemptReq.Context(), "HTTP "+attemptReq.Method, trace.WithSpanKind(trace.SpanKindClient))
		span = started
		attemptReq = attemptReq.WithContext(spanCtx)
		c.telemetry.Propagator().Inject(spanCtx, propagationHeaderCarrier(attemptReq.Header))
		if c.serviceName != "" {
			span.SetAttributes(attribute.String("server.address", c.serviceName))
		}
		defer span.End()
	}
	resp, err := c.client.Do(attemptReq)
	if err != nil {
		if span != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "request failed")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			statusLabel = "timeout"
		}
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusLabel = fmt.Sprintf("%d", resp.StatusCode)
		_ = resp.Body.Close()
		if span != nil {
			span.RecordError(&HTTPStatusError{StatusCode: resp.StatusCode, Status: resp.Status})
			span.SetStatus(codes.Error, resp.Status)
		}
		return &HTTPStatusError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	statusLabel = "ok"
	*result = resp
	return nil
}

func isIdempotent(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

type propagationHeaderCarrier http.Header

func (h propagationHeaderCarrier) Get(key string) string { return http.Header(h).Get(key) }
func (h propagationHeaderCarrier) Set(key, value string) { http.Header(h).Set(key, value) }
func (h propagationHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	return keys
}
