package types

import "errors"

// Sentinel errors for common failure modes.
var (
	ErrNotFound            = errors.New("not found")
	ErrInvalidConfig       = errors.New("invalid configuration")
	ErrProviderUnavailable = errors.New("provider unavailable")
	ErrGuardBlocked        = errors.New("content blocked by safety guard")
	ErrToolExecution       = errors.New("tool execution failed")
	ErrPipelineHalted      = errors.New("pipeline halted")
	ErrTimeout             = errors.New("operation timed out")
	ErrStreamClosed        = errors.New("stream closed")
)

// ErrorToHTTP maps a sentinel error to an HTTP status code.
// Unknown errors default to 500.
func ErrorToHTTP(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return 404
	case errors.Is(err, ErrInvalidConfig):
		return 400
	case errors.Is(err, ErrProviderUnavailable):
		return 503
	case errors.Is(err, ErrGuardBlocked):
		return 403
	case errors.Is(err, ErrToolExecution):
		return 500
	case errors.Is(err, ErrPipelineHalted):
		return 422
	case errors.Is(err, ErrTimeout):
		return 504
	case errors.Is(err, ErrStreamClosed):
		return 499
	default:
		return 500
	}
}
