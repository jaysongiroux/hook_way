package queue

import (
	"context"
	"errors"
	"fmt"
)

type inlineDeliveryKey struct{}

// WithInlineDelivery marks ctx so an async webhook destination delivers on the
// caller's goroutine, with its normal retry policy, and returns the real
// outcome instead of queueing the message. Dead-lettering never happens on
// this path: the caller owns the failure. Redrives use it so the result they
// report is the destination's actual answer. Other destination types already
// publish synchronously and ignore the flag.
func WithInlineDelivery(ctx context.Context) context.Context {
	return context.WithValue(ctx, inlineDeliveryKey{}, true)
}

// InlineDelivery reports whether ctx was marked by WithInlineDelivery.
func InlineDelivery(ctx context.Context) bool {
	inline, _ := ctx.Value(inlineDeliveryKey{}).(bool)
	return inline
}

// DeliveryError is returned when a synchronous webhook delivery (delivery_mode
// sync, or any delivery under WithInlineDelivery) does not succeed. It carries
// the last attempt's outcome (4xx, 5xx, 429, timeout, or error) and the number
// of attempts, so callers can classify the failure without parsing text. Its
// message is the underlying error's message.
type DeliveryError struct {
	Outcome  string
	Attempts int
	Err      error
}

func (e *DeliveryError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("delivery failed (%s after %d attempts)", e.Outcome, e.Attempts)
	}
	return e.Err.Error()
}

func (e *DeliveryError) Unwrap() error { return e.Err }

// ErrTargetNotConfigured is matched by errors.Is when a publish names a
// target its destination does not have. Nothing was sent.
var ErrTargetNotConfigured = errors.New("target is not configured")

// targetNotConfiguredError keeps the destination-specific message while
// matching ErrTargetNotConfigured.
type targetNotConfiguredError struct{ kind, destID, target string }

func (e targetNotConfiguredError) Error() string {
	return fmt.Sprintf("%s %q: target %q is not configured", e.kind, e.destID, e.target)
}

func (e targetNotConfiguredError) Is(target error) bool { return target == ErrTargetNotConfigured }
