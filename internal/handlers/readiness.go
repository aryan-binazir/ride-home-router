package handlers

import (
	"context"
	"log"
	"net/http"
	"time"
)

const readinessTimeout = 2 * time.Second

// HandleReadinessCheck handles GET /api/v1/ready.
func (h *Handler) HandleReadinessCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()

	status := "ready"
	code := http.StatusOK
	if err := h.DB.ReadinessCheck(ctx); err != nil {
		log.Printf("[ERROR] Readiness check failed: err=%v", err)
		status = "not_ready"
		code = http.StatusServiceUnavailable
	}
	h.writeJSON(w, code, map[string]string{"status": status})
}
