package handlers

import (
	"context"
	"ride-home-router/internal/plandraft"
)

func (l mobilePlanLifecycle) EditInputs(id string, edit func(*mobilePlanInputs)) plandraft.Draft {
	d, err := l.EditInputsContext(context.Background(), id, edit)
	if err != nil {
		panic(err)
	}
	return d
}

func (l mobilePlanLifecycle) AdoptCalculation(id string, draft plandraft.Draft, sessionID string) mobilePlanAdoption {
	result, err := l.AdoptCalculationContext(context.Background(), id, draft, sessionID)
	if err != nil {
		panic(err)
	}
	return result
}

func (l mobilePlanLifecycle) ReleaseSavedSession(id, sessionID string) {
	if err := l.ReleaseSavedSessionContext(context.Background(), id, sessionID); err != nil {
		panic(err)
	}
}
