package temporal

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDialRejectsMissingAddress(t *testing.T) {
	t.Parallel()

	_, err := Dial(context.Background(), Config{})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestDialReturnsContextErrorBeforeSDKDial(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Dial(ctx, Config{Address: "localhost:7233"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "dial temporal") {
		t.Fatalf("error = %q, want dial temporal context", err.Error())
	}
}
