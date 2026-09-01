package apperr

import (
	"errors"
	"testing"

	"github.com/baowk/dilu-go-kit/resp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestErrorMappingKeepsPublicMessage(t *testing.T) {
	err := Wrap(resp.CodeServiceDown, "外部服务不可用", errors.New("dsn/password must not escape")).WithRetryable(true)
	if CodeOf(err) != resp.CodeServiceDown || !Retryable(err) {
		t.Fatalf("unexpected error metadata: %+v", err)
	}
	if got := PublicMessage(err); got != "外部服务不可用" {
		t.Fatalf("public message = %q", got)
	}
	if got := status.Code(GRPCStatus(err)); got != codes.Internal {
		t.Fatalf("grpc code = %s", got)
	}
}
