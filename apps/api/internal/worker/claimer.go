package worker

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

type claimer struct {
	store         workflow.DurableRunStore
	ownerID       string
	leaseDuration time.Duration
	claimInterval time.Duration
	random        func() float64
}

func newClaimer(store workflow.DurableRunStore, ownerID string, leaseDuration, claimInterval time.Duration, random func() float64) *claimer {
	if random == nil {
		random = rand.Float64
	}
	return &claimer{store: store, ownerID: ownerID, leaseDuration: leaseDuration, claimInterval: claimInterval, random: random}
}

func (claimer *claimer) claim(ctx context.Context) (workflow.ClaimedRun, bool, error) {
	return claimer.store.ClaimRun(ctx, claimer.ownerID, claimer.leaseDuration)
}

func (claimer *claimer) retryDelay() time.Duration {
	random := claimer.random()
	if random < 0 {
		random = 0
	} else if random > 1 {
		random = 1
	}
	return time.Duration(float64(claimer.claimInterval) * (0.8 + 0.4*random))
}
