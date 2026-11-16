package server

import (
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"ride-home-router/internal/handlers"
	"ride-home-router/web"
	"strings"
	"testing"
)

// Run ASSET_CACHE_MEASURE=1 go test ./internal/server -run TestAssetCacheWarmVisit -v -count=3.
// curl has no browser cache: this client explicitly applies the response freshness
// policy on a second visit five hours later. It measures origin requests, not paint.
func TestAssetCacheWarmVisit(t *testing.T) {
	if os.Getenv("ASSET_CACHE_MEASURE") == "" {
		t.Skip("opt-in curl measurement")
	}
	server := httptest.NewServer(setupRoutes(&handlers.Handler{}, web.Static))
	defer server.Close()
	names := []string{"css/style.css", "css/editors.css", "js/htmx.min.js", "js/event-planner.js", "js/ui.js", "js/roster.js", "js/navigation.js", "js/editors.js", "js/auth.js"}
	cold, warm, revalidations := 0, 0, 0
	for _, name := range names {
		headers := filepath.Join(t.TempDir(), "headers")
		target := server.URL + web.AssetURL(name)
		request := func(args ...string) string {
			cmd := exec.CommandContext(t.Context(), "curl", append([]string{"--silent", "--show-error", "--fail", "--dump-header", headers, "--output", os.DevNull}, append(args, target)...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("curl: %v: %s", err, out)
			}
			b, err := os.ReadFile(headers)
			if err != nil {
				t.Fatal(err)
			}
			return strings.ToLower(string(b))
		}
		h := request()
		cold++
		if strings.Contains(h, "cache-control: public, max-age=31536000, immutable") {
			continue
		}
		request()
		warm++
		// Model the finder's expired-CDN case separately: a validator supplied by
		// the CDN would require this same round trip. The local origin has none.
		revalidations++
	}
	load, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("cold=%d warm_origin_requests=%d expected_expired_CDN_revalidations=%d load=%s", cold, warm, revalidations, load)
	if cold != len(names) {
		t.Fatal("incomplete cold visit")
	}
}
