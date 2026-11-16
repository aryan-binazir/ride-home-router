package planeval

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/routing"
	"testing"
)

// TestEvaluationPerformance captures timing logs and deterministic artifacts on main too.
func TestEvaluationPerformance(t *testing.T) {
	out := os.Getenv("PLANNER_PERF_DIR")
	if out == "" {
		t.Skip("set PLANNER_PERF_DIR to capture evaluation evidence")
	}
	previous := log.Writer()
	log.SetOutput(os.Stderr)
	t.Cleanup(func() { log.SetOutput(previous) })
	results, err := Run(context.Background(), func() routing.Router { return routing.NewBalancedRouter(distance.NewEstimator()) }, Scenarios(), 3)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var baseline []Result
	if err := json.Unmarshal(data, &baseline); err != nil {
		t.Fatal(err)
	}
	if regressions := Compare(baseline, results); len(regressions) != 0 {
		t.Fatalf("regressions: %v", regressions)
	}
	for i := range results {
		results[i].Metrics.SolveMs = 0
	}
	data, err = json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{"results.json": append(data, '\n'), "report.md": []byte(Report(baseline, results))} {
		if err := os.WriteFile(filepath.Join(out, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
