package engine

import "context"

type Observer interface {
	Observe(context.Context, Event) error
}

type discardObserver struct{}

func (discardObserver) Observe(context.Context, Event) error {
	return nil
}
