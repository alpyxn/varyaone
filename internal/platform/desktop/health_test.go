package desktop

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitForReadyWaitsUntilEndpointIsHealthy(t *testing.T) {
	attempts := 0
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := waitForReady(ctx, time.Millisecond, func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("not ready")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestWaitForReadyHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForReady(ctx, time.Millisecond, func(context.Context) error {
		return errors.New("not ready")
	}); err == nil {
		t.Fatal("expected cancellation error")
	}
}
