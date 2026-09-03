package metadata

import (
	"context"
	"net/http"
	"testing"

	grpcmetadata "google.golang.org/grpc/metadata"
)

func TestHTTPRoundTrip(t *testing.T) {
	ctx := With(context.Background(), Values{TraceID: "trace-1"})
	header := make(http.Header)
	InjectHTTP(ctx, header)
	got := From(ExtractHTTP(context.Background(), header))
	if got.TraceID != "trace-1" {
		t.Fatalf("metadata = %+v", got)
	}
}

func TestGRPCRoundTrip(t *testing.T) {
	ctx := With(context.Background(), Values{TraceID: "trace-1"})
	ctx = InjectGRPC(ctx)
	md, ok := grpcmetadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("missing outgoing metadata")
	}
	got := From(ExtractGRPC(context.Background(), md))
	if got.TraceID != "trace-1" {
		t.Fatalf("metadata = %+v", got)
	}
}

func TestExtractDoesNotTrustIdentityHeaders(t *testing.T) {
	header := http.Header{"X-User-Id": []string{"999"}, "X-Trace-Id": []string{"trace"}}
	got := From(ExtractHTTP(context.Background(), header))
	if got.TraceID != "trace" {
		t.Fatalf("metadata = %+v", got)
	}
}

func TestIdentityInjectionIsExplicit(t *testing.T) {
	ctx := With(context.Background(), Values{
		TraceID:     "trace-1",
		UserID:      7,
		TenantID:    8,
		WorkspaceID: 9,
		ShopIDs:     []int64{10, 11},
		Scopes:      []string{"read", "write", "bad\r\nvalue"},
	})
	header := make(http.Header)
	InjectIdentityHTTP(ctx, header)
	if header.Get("X-Trace-Id") != "" {
		t.Fatal("identity injection must not implicitly inject trace metadata")
	}
	if header.Get("X-User-Id") != "7" || header.Get("X-Workspace-Id") != "9" || header.Get("X-Shop-Ids") != "10,11" {
		t.Fatalf("headers = %v", header)
	}

	grpcCtx := InjectIdentityGRPC(ctx)
	md, ok := grpcmetadata.FromOutgoingContext(grpcCtx)
	if !ok || len(md.Get("x-user-id")) != 1 || md.Get("x-user-id")[0] != "7" || len(md.Get("x-scopes")) != 1 || md.Get("x-scopes")[0] != "read,write" {
		t.Fatalf("grpc metadata = %v", md)
	}
}

func TestWithCopiesSlices(t *testing.T) {
	shops := []int64{1}
	scopes := []string{"read"}
	ctx := With(context.Background(), Values{ShopIDs: shops, Scopes: scopes})
	shops[0] = 2
	scopes[0] = "write"
	got := From(ctx)
	if got.ShopIDs[0] != 1 || got.Scopes[0] != "read" {
		t.Fatalf("metadata slices were not copied: %+v", got)
	}
}

func TestExtractPreservesAuthenticatedIdentity(t *testing.T) {
	base := With(context.Background(), Values{UserID: 42, WorkspaceID: 99})
	got := From(ExtractHTTP(base, http.Header{"X-Trace-Id": []string{"trace"}}))
	if got.TraceID != "trace" || got.UserID != 42 || got.WorkspaceID != 99 {
		t.Fatalf("metadata = %+v", got)
	}
}
