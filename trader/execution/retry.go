package execution

import (
	"context"
	"time"
)

// RetryableError describes a safe, bounded read retry. It never authorizes
// resubmitting a mutation whose outcome may be unknown.
type RetryableError struct {
	Message string
	Delay   time.Duration
}

func (e *RetryableError) Error() string { return e.Message }

type protectivePriorityKey struct{}

func WithProtectivePriority(ctx context.Context) context.Context {
	return context.WithValue(ctx, protectivePriorityKey{}, true)
}
func ProtectivePriority(ctx context.Context) bool {
	v, _ := ctx.Value(protectivePriorityKey{}).(bool)
	return v
}
