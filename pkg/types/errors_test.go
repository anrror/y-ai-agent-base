package types

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorToHTTP_NotFound(t *testing.T) {
	assert.Equal(t, 404, ErrorToHTTP(ErrNotFound))
	assert.Equal(t, 404, ErrorToHTTP(fmt.Errorf("wrap: %w", ErrNotFound)))
}

func TestErrorToHTTP_InvalidConfig(t *testing.T) {
	assert.Equal(t, 400, ErrorToHTTP(ErrInvalidConfig))
}

func TestErrorToHTTP_ProviderUnavailable(t *testing.T) {
	assert.Equal(t, 503, ErrorToHTTP(ErrProviderUnavailable))
}

func TestErrorToHTTP_GuardBlocked(t *testing.T) {
	assert.Equal(t, 403, ErrorToHTTP(ErrGuardBlocked))
}

func TestErrorToHTTP_ToolExecution(t *testing.T) {
	assert.Equal(t, 500, ErrorToHTTP(ErrToolExecution))
}

func TestErrorToHTTP_PipelineHalted(t *testing.T) {
	assert.Equal(t, 422, ErrorToHTTP(ErrPipelineHalted))
}

func TestErrorToHTTP_Timeout(t *testing.T) {
	assert.Equal(t, 504, ErrorToHTTP(ErrTimeout))
}

func TestErrorToHTTP_StreamClosed(t *testing.T) {
	assert.Equal(t, 499, ErrorToHTTP(ErrStreamClosed))
}

func TestErrorToHTTP_Unknown(t *testing.T) {
	assert.Equal(t, 500, ErrorToHTTP(errors.New("something unexpected")))
}

func TestErrorToHTTP_Nil(t *testing.T) {
	assert.Equal(t, 500, ErrorToHTTP(nil))
}

func TestSentinelErrors_AreDistinct(t *testing.T) {
	errs := []error{
		ErrNotFound,
		ErrInvalidConfig,
		ErrProviderUnavailable,
		ErrGuardBlocked,
		ErrToolExecution,
		ErrPipelineHalted,
		ErrTimeout,
		ErrStreamClosed,
	}
	for i, e1 := range errs {
		for j, e2 := range errs {
			if i == j {
				assert.True(t, errors.Is(e1, e2), "error should match itself: %v", e1)
			} else {
				assert.False(t, errors.Is(e1, e2), "error %v should not match %v", e1, e2)
			}
		}
	}
}
