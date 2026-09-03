// Package metadata carries request-scoped transport-neutral fields.
//
// Only the trace identifier is propagated automatically. Identity fields are
// populated after authentication and can be propagated explicitly by trusted
// service clients; this prevents unauthenticated headers from becoming an
// authority boundary.
package metadata

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	grpcmetadata "google.golang.org/grpc/metadata"
)

// Values contains request metadata safe for use by the application layer.
type Values struct {
	TraceID     string
	UserID      int64
	TenantID    int64
	WorkspaceID int64
	ShopIDs     []int64
	Scopes      []string
}

type contextKey struct{}

// With returns a context containing normalized metadata values.
func With(ctx context.Context, values Values) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	values.TraceID = normalize(values.TraceID)
	values.ShopIDs = append([]int64(nil), values.ShopIDs...)
	values.Scopes = append([]string(nil), values.Scopes...)
	return context.WithValue(ctx, contextKey{}, values)
}

// From returns metadata stored in ctx. A zero value is returned for nil or
// contexts without metadata.
func From(ctx context.Context) Values {
	if ctx == nil {
		return Values{}
	}
	values, _ := ctx.Value(contextKey{}).(Values)
	return values
}

// InjectHTTP adds the trace ID to an outgoing HTTP request.
func InjectHTTP(ctx context.Context, header http.Header) {
	if header == nil {
		return
	}
	values := From(ctx)
	if values.TraceID != "" {
		header.Set("X-Trace-Id", values.TraceID)
	}
}

// InjectIdentityHTTP adds authenticated identity metadata to an outgoing
// request. Call this only for trusted service-to-service calls.
func InjectIdentityHTTP(ctx context.Context, header http.Header) {
	if header == nil {
		return
	}
	values := From(ctx)
	if values.UserID > 0 {
		header.Set("X-User-Id", formatInt(values.UserID))
	}
	if values.TenantID > 0 {
		header.Set("X-Tenant-Id", formatInt(values.TenantID))
	}
	if values.WorkspaceID > 0 {
		header.Set("X-Workspace-Id", formatInt(values.WorkspaceID))
	}
	if len(values.ShopIDs) > 0 {
		header.Set("X-Shop-Ids", joinInt64(values.ShopIDs))
	}
	if len(values.Scopes) > 0 {
		header.Set("X-Scopes", joinScopes(values.Scopes))
	}
}

// ExtractHTTP extracts the trace ID from inbound HTTP headers. It does
// not extract user or tenant identity; authentication must establish those
// values separately.
func ExtractHTTP(ctx context.Context, header http.Header) context.Context {
	values := From(ctx)
	if header != nil {
		values.TraceID = first(header.Get("X-Trace-Id"), header.Get("X-Request-Id")) // legacy alias
	}
	return With(ctx, values)
}

// InjectGRPC adds metadata to an outgoing gRPC context.
func InjectGRPC(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	values := From(ctx)
	md, _ := grpcmetadata.FromOutgoingContext(ctx)
	md = md.Copy()
	if values.TraceID != "" {
		md.Set("x-trace-id", values.TraceID)
	}
	return grpcmetadata.NewOutgoingContext(ctx, md)
}

// InjectIdentityGRPC adds authenticated identity metadata to an outgoing gRPC
// context. Call this only for trusted service-to-service calls.
func InjectIdentityGRPC(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	values := From(ctx)
	md, _ := grpcmetadata.FromOutgoingContext(ctx)
	md = md.Copy()
	if values.UserID > 0 {
		md.Set("x-user-id", formatInt(values.UserID))
	}
	if values.TenantID > 0 {
		md.Set("x-tenant-id", formatInt(values.TenantID))
	}
	if values.WorkspaceID > 0 {
		md.Set("x-workspace-id", formatInt(values.WorkspaceID))
	}
	if len(values.ShopIDs) > 0 {
		md.Set("x-shop-ids", joinInt64(values.ShopIDs))
	}
	if len(values.Scopes) > 0 {
		md.Set("x-scopes", joinScopes(values.Scopes))
	}
	return grpcmetadata.NewOutgoingContext(ctx, md)
}

// ExtractGRPC extracts the trace ID from inbound gRPC metadata.
func ExtractGRPC(ctx context.Context, md grpcmetadata.MD) context.Context {
	values := From(ctx)
	if md != nil {
		values.TraceID = firstValue(md.Get("x-trace-id"), md.Get("x-request-id")) // legacy alias
	}
	return With(ctx, values)
}

func first(values ...string) string {
	for _, value := range values {
		if value = normalize(value); value != "" {
			return value
		}
	}
	return ""
}

func firstValue(values ...[]string) string {
	for _, list := range values {
		if len(list) > 0 {
			if value := normalize(list[0]); value != "" {
				return value
			}
		}
	}
	return ""
}

func normalize(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		return ""
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			continue
		}
		return ""
	}
	return value
}

func formatInt(value int64) string { return strconv.FormatInt(value, 10) }

func joinInt64(values []int64) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value > 0 {
			parts = append(parts, formatInt(value))
		}
	}
	return strings.Join(parts, ",")
}

func joinScopes(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n,") {
			continue
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, ",")
}
