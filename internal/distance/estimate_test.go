package distance

import (
	"context"
	"math"
	"ride-home-router/internal/models"
	"testing"
)

func TestEstimatorScalesStraightLineDistanceWithoutProviderCalls(t *testing.T) {
	estimator := NewEstimator()
	result, err := estimator.GetDistance(context.Background(), models.Coordinates{Lat: 0, Lng: 0}, models.Coordinates{Lat: 1, Lng: 0})
	if err != nil {
		t.Fatalf("GetDistance() error = %v", err)
	}
	if math.Abs(result.DistanceMeters-144553.4) > 1 {
		t.Fatalf("DistanceMeters = %.1f, want 111194.9 × 1.3 = 144553.4", result.DistanceMeters)
	}
	if math.Abs(result.DurationSecs-12929.6) > 1 {
		t.Fatalf("DurationSecs = %.1f, want 144553.4 ÷ 11.18 = 12929.6", result.DurationSecs)
	}
	same, err := estimator.GetDistance(context.Background(), models.Coordinates{Lat: 42.36, Lng: -71.06}, models.Coordinates{Lat: 42.36, Lng: -71.06})
	if err != nil || same.DistanceMeters != 0 || same.DurationSecs != 0 {
		t.Fatalf("same point = (%+v, %v), want zeros", same, err)
	}
	if err := estimator.PrewarmPairs(context.Background(), []DistancePair{{Origin: models.Coordinates{Lat: 1}, Destination: models.Coordinates{Lat: 2}}}); err != nil {
		t.Fatalf("PrewarmPairs() error = %v, want no-op", err)
	}
	if _, ok := estimator.(interface{ NoPrewarm() bool }); !ok {
		t.Fatal("estimator should advertise that it needs no prewarming")
	}
}

func TestEstimatorRejectsInvalidCoordinates(t *testing.T) {
	estimator := NewEstimator()
	for name, coords := range map[string]models.Coordinates{
		"nan":       {Lat: math.NaN(), Lng: 0},
		"latitude":  {Lat: 91, Lng: 0},
		"longitude": {Lat: 0, Lng: 181},
	} {
		if _, err := estimator.GetDistance(context.Background(), coords, models.Coordinates{}); err == nil {
			t.Fatalf("%s: GetDistance() error = nil, want rejection", name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := estimator.GetDistance(ctx, models.Coordinates{}, models.Coordinates{Lat: 1}); err == nil {
		t.Fatal("cancelled context should fail")
	}
}
