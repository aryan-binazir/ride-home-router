package server

import (
	"context"
	"ride-home-router/internal/database"
	"testing"
)

type fakeCache struct {
	database.DistanceCacheRepository
}

func TestRoutingEngineSelection(t *testing.T) {
	estimate, providerFree, err := routingDistanceSource("", nil, nil)
	if err != nil {
		t.Fatalf("default engine error = %v", err)
	}
	if _, ok := estimate.(interface{ NoPrewarm() bool }); !ok || !providerFree {
		t.Fatalf("default engine = %T providerFree=%v, want the estimator", estimate, providerFree)
	}
	if _, _, err := routingDistanceSource("carrier-pigeon", nil, nil); err == nil {
		t.Fatal("unknown engine should be rejected")
	}
	matrix, providerFree, err := routingDistanceSource("matrix", fakeCache{}, func(context.Context) (string, error) { return "k", nil })
	if err != nil {
		t.Fatalf("matrix engine error = %v", err)
	}
	if _, ok := matrix.(interface{ NoPrewarm() bool }); ok || providerFree {
		t.Fatalf("matrix engine = %T providerFree=%v, want the Google calculator", matrix, providerFree)
	}
	_ = matrix
}
