package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"ride-home-router/internal/postgres/postgrestest"
	"strings"
	"testing"
)

func TestMobileDraftSurvivesAnotherInstanceAndRestart(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	start := func() (*Server, string) {
		t.Helper()
		s, err := New(t.Context(), Config{Addr: "127.0.0.1:0", DatabaseURL: databaseURL})
		if err != nil {
			t.Fatal(err)
		}
		addr, err := s.Start()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
		return s, "http://" + addr
	}
	first, a := start()
	_, b := start()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, a+"/m/plan/when", strings.NewReader(url.Values{"route_time": {"06:45"}, "mode": {"pickup"}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cookies := resp.Cookies()
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || len(cookies) == 0 {
		t.Fatalf("create draft status %d cookies %v", resp.StatusCode, cookies)
	}
	check := func(base string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/m/plan/when", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cookies {
			req.AddCookie(c)
		}
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if r.StatusCode != http.StatusOK || !strings.Contains(string(body), `value="06:45"`) {
			t.Fatalf("draft did not survive instance switch: status %d", r.StatusCode)
		}
	}
	check(b)
	if err := first.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, restarted := start()
	check(restarted)
}
