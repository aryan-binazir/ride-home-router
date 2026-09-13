package server

import (
	"context"
	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"testing"
)

type fakeCache struct {
	database.DistanceCacheRepository
}

func TestRoutingEngineSelection(t *testing.T) {
	estimate, err := routingDistanceSource("", nil, nil)
	if err != nil {
		t.Fatalf("default engine error = %v", err)
	}
	if _, ok := estimate.(interface{ NoPrewarm() bool }); !ok {
		t.Fatalf("default engine = %T, want the estimator", estimate)
	}
	if _, err := routingDistanceSource("carrier-pigeon", nil, nil); err == nil {
		t.Fatal("unknown engine should be rejected")
	}
	matrix, err := routingDistanceSource("matrix", fakeCache{}, func(context.Context) (string, error) { return "k", nil })
	if err != nil {
		t.Fatalf("matrix engine error = %v", err)
	}
	if _, ok := matrix.(interface{ NoPrewarm() bool }); ok {
		t.Fatalf("matrix engine = %T, want the Google calculator", matrix)
	}
	var _ distance.SolveSource = matrix
}
