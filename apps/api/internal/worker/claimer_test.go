package worker

import (
	"testing"
	"time"
)

func TestClaimerRetryDelayUsesBoundedJitter(t *testing.T) {
	for _, test := range []struct {
		name   string
		random float64
		want   time.Duration
	}{
		{name: "lower bound", random: 0, want: 800 * time.Millisecond},
		{name: "middle", random: 0.5, want: time.Second},
		{name: "upper bound", random: 1, want: 1200 * time.Millisecond},
		{name: "clamp low", random: -4, want: 800 * time.Millisecond},
		{name: "clamp high", random: 9, want: 1200 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			claimer := newClaimer(nil, "worker", time.Second, time.Second, func() float64 { return test.random })
			if got := claimer.retryDelay(); got != test.want {
				t.Fatalf("delay=%s want=%s", got, test.want)
			}
		})
	}
}
