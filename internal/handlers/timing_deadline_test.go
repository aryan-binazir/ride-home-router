package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"ride-home-router/internal/orderedroute"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type deadlineMeasurer struct {
	t     *testing.T
	want  time.Duration
	calls int
}

func (m *deadlineMeasurer) Measure(ctx context.Context, requests []orderedroute.Request) []orderedroute.Result {
	m.calls++
	deadline, ok := ctx.Deadline()
	if !ok {
		m.t.Error("measurement has no deadline")
	} else {
		if got := time.Until(deadline); got != m.want {
			m.t.Errorf("measurement budget = %s, want %s", got, m.want)
		}
		<-ctx.Done()
	}
	results := make([]orderedroute.Result, len(requests))
	for i, r := range requests {
		results[i] = orderedroute.Result{ID: r.ID, Err: errors.New("measurement canceled")}
	}
	return results
}

func TestTimingRequestsBoundMeasurementLifetime(t *testing.T) {
	for _, tc := range []struct {
		name         string
		caller, want time.Duration
		edit, cancel bool
	}{
		{name: "refresh without deadline", want: 30 * time.Second},
		{name: "edit without deadline", want: 30 * time.Second, edit: true},
		{name: "earlier caller budget", caller: 10 * time.Second, want: 8 * time.Second},
		{name: "insufficient caller budget", caller: 4 * time.Second},
		{name: "caller cancellation", want: 30 * time.Second, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, session := newRouteEditHandler(t)
				m := &deadlineMeasurer{t: t, want: tc.want}
				h.Measurer = m
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if tc.caller > 0 {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, tc.caller)
					defer stop()
				}
				if tc.cancel {
					timer := time.AfterFunc(time.Second, cancel)
					defer timer.Stop()
				}
				req := newRouteEditJSONRequest("/api/v1/routes/session/timings", []byte(fmt.Sprintf(`{"session_id":%q,"route_index":0}`, session.ID))).WithContext(ctx)
				if tc.edit {
					req = newRouteEditJSONRequest("/api/v1/routes/edit/swap-drivers", []byte(fmt.Sprintf(`{"session_id":%q,"route_index_1":0,"route_index_2":1}`, session.ID))).WithContext(ctx)
				}
				req.Header.Set("HX-Request", "true")
				w := httptest.NewRecorder()
				start := time.Now()
				if tc.edit {
					h.HandleSwapDrivers(w, req)
				} else {
					h.HandleRouteTimings(w, req)
				}
				wantElapsed := tc.want
				if tc.cancel {
					wantElapsed = time.Second
				}
				if got := time.Since(start); got != wantElapsed {
					t.Errorf("handler lifetime = %s, want %s", got, wantElapsed)
				}
				wantCalls := 1
				if tc.want == 0 {
					wantCalls = 0
				}
				if m.calls != wantCalls {
					t.Errorf("measure calls=%d, want %d", m.calls, wantCalls)
				}
				if w.Code != 200 || !strings.Contains(w.Body.String(), "Could not get timings.") {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
			})
		})
	}
}
