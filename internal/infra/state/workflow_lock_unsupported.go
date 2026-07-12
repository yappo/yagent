//go:build !darwin && !linux

package state

import (
	"context"
	"fmt"
	"runtime"
)

func acquireWorkflowLock(_ context.Context, _ string) (func() error, error) {
	return nil, fmt.Errorf("durable workflow storage is unsupported on %s: advisory file locking is unavailable", runtime.GOOS)
}
