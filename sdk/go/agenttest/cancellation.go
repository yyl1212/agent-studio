package agenttest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

const (
	defaultCancelAfter = 10 * time.Millisecond
	defaultMaxWait     = 250 * time.Millisecond
)

type CancellationCase struct {
	Request     agentnode.Request
	CancelAfter time.Duration
	MaxWait     time.Duration
}

func validateCancellation(node agentnode.Node, cancellation CancellationCase) error {
	cancelAfter := cancellation.CancelAfter
	if cancelAfter <= 0 {
		cancelAfter = defaultCancelAfter
	}
	maxWait := cancellation.MaxWait
	if maxWait <= 0 {
		maxWait = defaultMaxWait
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := node.Execute(ctx, cancellation.Request)
		result <- err
	}()

	timer := time.NewTimer(cancelAfter)
	defer timer.Stop()
	select {
	case err := <-result:
		return fmt.Errorf("execute returned before cancellation: %w", err)
	case <-timer.C:
		cancel()
	}

	wait := time.NewTimer(maxWait)
	defer wait.Stop()
	select {
	case err := <-result:
		if errors.Is(err, context.Canceled) || agentnode.KindOf(err) == agentnode.ErrorKindCanceled {
			return nil
		}
		return fmt.Errorf("execute returned non-canceled error after cancellation: %w", err)
	case <-wait.C:
		return fmt.Errorf("execute did not return within %s after cancellation", maxWait)
	}
}
