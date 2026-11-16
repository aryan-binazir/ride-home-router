package orderedroute

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"ride-home-router/internal/database"
	"strings"
	"sync"
	"testing"
)

type quotaOutcome struct {
	granted uint16
	used    int
}

type quotaState struct {
	done    uint16
	granted uint16
	started uint8
	used    int
}

func enumerateQuotaOutcomes(costs [][]int, ceiling int, batch bool) map[quotaOutcome]bool {
	var units []int
	masks := make([]uint16, len(costs))
	totals := make([]int, len(costs))
	for request, groups := range costs {
		for _, cost := range groups {
			masks[request] |= 1 << len(units)
			units = append(units, cost)
			totals[request] += cost
		}
	}
	all := uint16(1<<len(units)) - 1
	outcomes := make(map[quotaOutcome]bool)
	seen := make(map[quotaState]bool)
	var walk func(quotaState)
	walk = func(state quotaState) {
		if seen[state] {
			return
		}
		seen[state] = true
		if state.done == all {
			outcomes[quotaOutcome{state.granted, state.used}] = true
			return
		}
		for request, mask := range masks {
			if state.done&mask == mask {
				continue
			}
			if batch && state.started&(1<<request) == 0 {
				next := state
				next.started |= 1 << request
				if next.used+totals[request] <= ceiling {
					next.done |= mask
					next.granted |= mask
					next.used += totals[request]
				}
				walk(next)
				continue
			}
			for group, cost := range units {
				bit := uint16(1 << group)
				if mask&bit == 0 || state.done&bit != 0 {
					continue
				}
				next := state
				next.done |= bit
				if next.used+cost <= ceiling {
					next.granted |= bit
					next.used += cost
				}
				walk(next)
			}
		}
	}
	walk(quotaState{})
	return outcomes
}

func TestReservationConcurrentQuotaOutcomesReachable(t *testing.T) {
	scenarios, candidateOutcomes := 0, 0
	check := func(costs [][]int) {
		total := 0
		for _, groups := range costs {
			for _, cost := range groups {
				total += cost
			}
		}
		for ceiling := 0; ceiling <= total+1; ceiling++ {
			main := enumerateQuotaOutcomes(costs, ceiling, false)
			candidate := enumerateQuotaOutcomes(costs, ceiling, true)
			scenarios++
			candidateOutcomes += len(candidate)
			actual := measureConcurrentQuotaOutcome(t, costs, ceiling)
			if !candidate[actual] || !main[actual] {
				t.Fatalf("actual client outcome=%v costs=%v ceiling=%d candidate=%v main=%v", actual, costs, ceiling, candidate, main)
			}
			for outcome := range candidate {
				if !main[outcome] {
					t.Fatalf("costs=%v ceiling=%d candidate=%v main=%v", costs, ceiling, outcome, main)
				}
			}
		}
	}
	for requests := 2; requests <= 3; requests++ {
		maxCost := 3
		if requests == 3 {
			maxCost = 2
		}
		flat := make([]int, requests*2)
		var generate func(int)
		generate = func(position int) {
			if position == len(flat) {
				costs := make([][]int, requests)
				for i := range costs {
					costs[i] = flat[2*i : 2*i+2]
				}
				check(costs)
				return
			}
			for cost := 1; cost <= maxCost; cost++ {
				flat[position] = cost
				generate(position + 1)
			}
		}
		generate(0)
	}
	check([][]int{{2, 2}, {3}})
	check([][]int{{2, 3}, {4, 5}})
	t.Logf("%d ceiling/cost scenarios, %d candidate outcomes, all within main's enumerated reachable set", scenarios, candidateOutcomes)
	for _, costs := range [][][]int{{{2, 2}, {3}}, {{2, 3}, {4, 5}}, {{1, 2}, {2, 1}, {1, 1}}} {
		t.Log(fmt.Sprint(costs))
	}
}

func measureConcurrentQuotaOutcome(t *testing.T, costs [][]int, ceiling int) quotaOutcome {
	t.Helper()
	var mu sync.Mutex
	used := 0
	reserve := func(_ context.Context, units int) error {
		mu.Lock()
		defer mu.Unlock()
		if used+units > ceiling {
			return database.ErrUsageExhausted
		}
		used += units
		return nil
	}
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload computeRoutesRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		legs := make([]string, len(payload.Intermediates)+1)
		for i := range legs {
			legs[i] = `{"distanceMeters":1,"duration":"1s"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"routes":[{"legs":[` + strings.Join(legs, ",") + `]}]}`))}, nil
	})
	var wg sync.WaitGroup
	results := make([][]Result, len(costs))
	for request, groups := range costs {
		wg.Go(func() {
			var input []Request
			for car, cost := range groups {
				input = append(input, Request{ID: fmt.Sprint(car), Points: points((cost-1)*11 + 2)})
			}
			client := newClient(staticKey("synthetic"), reserve, &http.Client{Transport: transport}, "http://synthetic")
			results[request] = client.Measure(t.Context(), input)
		})
	}
	wg.Wait()
	outcome := quotaOutcome{used: used}
	position, grantedUnits := 0, 0
	for request, output := range results {
		for group, result := range output {
			if result.Err == nil {
				outcome.granted |= 1 << position
				grantedUnits += costs[request][group]
			} else if result.Err.Error() != "orderedroute: google usage ceiling reached for this month" {
				t.Fatal(result.Err)
			}
			position++
		}
	}
	if used != grantedUnits || used > ceiling {
		t.Fatalf("reserved=%d granted=%d ceiling=%d", used, grantedUnits, ceiling)
	}
	return outcome
}
