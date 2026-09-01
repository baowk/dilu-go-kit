// Package apperr defines transport-neutral application errors.
package apperr

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/baowk/dilu-go-kit/resp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Error contains a stable public code/message and an optional internal cause.
// Message is intended for clients; details from Cause must only be logged.
type Error struct {
	Code      int
	Message   string
	Cause     error
	Retryable bool
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("app error %d: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("app error %d: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// New creates a public application error.
func New(code int, message string) *Error { return &Error{Code: code, Message: message} }

// Wrap creates an application error while retaining an internal cause.
func Wrap(code int, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

// WithRetryable marks an error as safe for a retry policy.
func (e *Error) WithRetryable(retryable bool) *Error {
	if e != nil {
		e.Retryable = retryable
	}
	return e
}

// CodeOf extracts the stable application code.
func CodeOf(err error) int {
	var appErr *Error
	if errors.As(err, &appErr) && appErr != nil {
		return appErr.Code
	}
	return 0
}

// PublicMessage returns a safe client-facing message.
func PublicMessage(err error) string {
	var appErr *Error
	if errors.As(err, &appErr) && appErr != nil && appErr.Message != "" {
		return appErr.Message
	}
	return "服务内部错误"
}

// Retryable reports whether an error was explicitly marked retryable.
func Retryable(err error) bool {
	var appErr *Error
	return errors.As(err, &appErr) && appErr != nil && appErr.Retryable
}

// HTTPStatus maps an application error code to an HTTP status.
func HTTPStatus(err error) int {
	if code := CodeOf(err); code != 0 {
		return resp.HTTPStatusForCode(code)
	}
	return http.StatusInternalServerError
}

// GRPCStatus maps an application error to a gRPC status error without
// exposing its internal cause.
func GRPCStatus(err error) error {
	if err == nil {
		return nil
	}
	code := CodeOf(err)
	if code == 0 {
		return status.Error(codes.Internal, "服务内部错误")
	}
	return status.Error(grpcCode(code), PublicMessage(err))
}

func grpcCode(code int) codes.Code {
	switch code / 100 {
	case 400:
		return codes.InvalidArgument
	case 401:
		return codes.Unauthenticated
	case 403:
		return codes.PermissionDenied
	case 404:
		return codes.NotFound
	case 409:
		return codes.Aborted
	case 429:
		return codes.ResourceExhausted
	case 500:
		return codes.Internal
	default:
		return codes.Unknown
	}
}
