package handlers

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	readinessTimeout = 2 * time.Second
	probeCacheTTL    = time.Second
)

// probeCache lets unauthenticated probe floods cost at most one database check per TTL.
type probeCache struct {
	mu      sync.Mutex
	checked time.Time
	err     error
}

func (p *probeCache) check(r *http.Request, probe func(context.Context) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Since(p.checked) < probeCacheTTL {
		return p.err
	}
	// A caller that disconnects must not cache a failure for everyone else.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), readinessTimeout)
	defer cancel()
	p.err = probe(ctx)
	p.checked = time.Now()
	return p.err
}

func (h *Handler) HandleReadinessCheck(w http.ResponseWriter, r *http.Request) {
	status := "ready"
	code := http.StatusOK
	if err := h.readinessProbe.check(r, h.DB.ReadinessCheck); err != nil {
		log.Printf("[ERROR] Readiness check failed: err=%v", err)
		status = "not_ready"
		code = http.StatusServiceUnavailable
	}
	h.writeJSON(w, code, map[string]string{"status": status})
}
