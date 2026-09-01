package clientx

import (
	"context"
	"errors"

	"github.com/baowk/dilu-go-kit/apperr"
	"github.com/baowk/dilu-go-kit/resp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// IsRetryable reports whether err is generally safe to retry.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, ErrCircuitOpen) {
		return false
	}
	if apperr.CodeOf(err) != 0 {
		return apperr.Retryable(err)
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}

// MapGRPCError maps gRPC errors to standard response codes.
func MapGRPCError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrCircuitOpen) {
		return NewError(resp.CodeServiceDown, "服务熔断", err)
	}
	switch status.Code(err) {
	case codes.OK:
		return nil
	case codes.InvalidArgument:
		return NewError(resp.CodeInvalidParam, "参数错误", err)
	case codes.Unauthenticated:
		return NewError(resp.CodeUnauthorized, "未登录", err)
	case codes.PermissionDenied:
		return NewError(resp.CodeForbidden, "无权操作", err)
	case codes.NotFound:
		return NewError(resp.CodeNotFound, "资源不存在", err)
	case codes.AlreadyExists, codes.Aborted:
		return NewError(resp.CodeConflict, "资源冲突", err)
	case codes.ResourceExhausted:
		return NewError(resp.CodeTooManyReqs, "请求过于频繁", err)
	case codes.Unavailable, codes.DeadlineExceeded:
		return NewError(resp.CodeServiceDown, "外部服务不可用", err)
	default:
		return NewError(resp.CodeInternal, "服务错误", err)
	}
}
