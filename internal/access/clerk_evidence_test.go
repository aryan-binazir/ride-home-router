package access_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"ride-home-router/internal/access"
	"ride-home-router/internal/access/accesstest"
	"ride-home-router/internal/logtest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type evidenceTransport struct {
	base                    http.RoundTripper
	sessionDelay, userDelay time.Duration
	sessions, users         atomic.Int32
}

func (e *evidenceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	delay := e.userDelay
	if strings.HasPrefix(r.URL.Path, "/v1/sessions/") {
		e.sessions.Add(1)
		delay = e.sessionDelay
	} else {
		e.users.Add(1)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	return e.base.RoundTrip(r)
}

func evidenceHandler(t testing.TB, cfg access.Config, allowed bool) http.Handler {
	t.Helper()
	a, err := access.New(cfg, &approvals{allowed: allowed})
	if err != nil {
		t.Fatal(err)
	}
	return a.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Principal", access.UserID(r.Context()))
		w.Header().Set("Admin", fmt.Sprint(access.IsAdmin(r.Context())))
		w.WriteHeader(204)
	}))
}

func evidenceRequest(h http.Handler, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/settings", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type evidenceResponse struct {
	Status  int
	Body    string
	Headers http.Header
	Logs    string
}

type evidenceRow struct {
	Name            string
	Responses       []evidenceResponse
	Sessions, Users int32
}

func TestEvidenceMatrix(t *testing.T) {
	f := accesstest.New(t)
	f.Admin()
	var rows []evidenceRow
	for _, session := range []string{"ok", "revoked", "expired", "error"} {
		for _, user := range []string{"ok", "missing", "unapproved", "error"} {
			for _, timing := range []struct {
				name          string
				session, user time.Duration
			}{
				{"session_first", time.Millisecond, 10 * time.Millisecond},
				{"user_first", 10 * time.Millisecond, time.Millisecond},
				{"together", time.Millisecond, time.Millisecond},
			} {
				name := fmt.Sprintf("%s/%s/%s", session, user, timing.name)
				t.Run(name, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						token := f.Token("user_admin", "sess_admin")
						f.Session("sess_admin", "user_admin", "active")
						f.Fail("/v1/sessions/sess_admin", 0)
						f.Fail("/v1/users/user_admin", 0)
						if session == "revoked" || session == "expired" {
							f.Session("sess_admin", "user_admin", session)
						}
						if session == "error" {
							f.Fail("/v1/sessions/sess_admin", 503)
						}
						if user == "missing" {
							f.Fail("/v1/users/user_admin", 404)
						}
						if user == "error" {
							f.Fail("/v1/users/user_admin", 503)
						}
						cfg := f.Config("other-admin@example.test")
						transport := &evidenceTransport{base: cfg.HTTPClient.Transport, sessionDelay: timing.session, userDelay: timing.user}
						cfg.HTTPClient.Transport = transport
						h := evidenceHandler(t, cfg, user != "unapproved")
						want := 204
						switch {
						case session == "error":
							want = 503
						case session != "ok":
							want = 401
						case user == "missing":
							want = 401
						case user == "error":
							want = 503
						case user == "unapproved":
							want = 403
						}
						row := evidenceRow{Name: name}
						var logs bytes.Buffer
						logtest.Capture(t, &logs)
						for range 3 {
							logs.Reset()
							w := evidenceRequest(h, token)
							if w.Code != want {
								t.Fatalf("status=%d want=%d", w.Code, want)
							}
							row.Responses = append(row.Responses, evidenceResponse{w.Code, w.Body.String(), w.Header().Clone(), logs.String()})
						}
						synctest.Wait()
						row.Sessions, row.Users = transport.sessions.Load(), transport.users.Load()
						rows = append(rows, row)
					})
				})
			}
		}
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("EVIDENCE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile("testdata/clerk-matrix.json", encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("EVIDENCE_UPDATE_GOLDEN") != "1" {
		baselineData, err := os.ReadFile("testdata/clerk-matrix.json")
		if err != nil {
			t.Fatal(err)
		}
		var baseline []evidenceRow
		if err := json.Unmarshal(baselineData, &baseline); err != nil {
			t.Fatal(err)
		}
		if len(rows) != len(baseline) {
			t.Fatal("matrix size mismatch")
		}
		responseMismatches, countMismatches := 0, 0
		for i, row := range rows {
			if row.Name != baseline[i].Name {
				t.Fatal("matrix case mismatch")
			}
			if !reflect.DeepEqual(row.Responses, baseline[i].Responses) {
				responseMismatches++
				t.Errorf("response mismatch %s", row.Name)
			}
			if row.Sessions != baseline[i].Sessions || row.Users != baseline[i].Users {
				countMismatches++
				if strings.HasPrefix(row.Name, "ok/") || row.Sessions != baseline[i].Sessions || row.Users < baseline[i].Users || row.Users > baseline[i].Users+row.Sessions {
					t.Errorf("unexpected call count change %s: sessions=%d users=%d baseline=%d/%d", row.Name, row.Sessions, row.Users, baseline[i].Sessions, baseline[i].Users)
				}
			}
		}
		t.Logf("cases=%d HTTP_body_headers_log_mismatches=%d call_count_mismatches=%d", len(rows), responseMismatches, countMismatches)
	}
}

func TestEvidenceConcurrentMisses(t *testing.T) {
	f := accesstest.New(t)
	token := f.Admin()
	cfg := f.Config()
	transport := &evidenceTransport{base: cfg.HTTPClient.Transport, sessionDelay: 10 * time.Millisecond, userDelay: 10 * time.Millisecond}
	cfg.HTTPClient.Transport = transport
	h := evidenceHandler(t, cfg, true)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 24 {
		workers.Go(func() {
			<-start
			if got := evidenceRequest(h, token).Code; got != 204 {
				t.Errorf("status=%d", got)
			}
		})
	}
	close(start)
	workers.Wait()
	if transport.sessions.Load() != 1 || transport.users.Load() != 1 {
		t.Fatalf("sessions=%d users=%d", transport.sessions.Load(), transport.users.Load())
	}
	t.Logf("24 simultaneous requests: session_calls=%d user_calls=%d", transport.sessions.Load(), transport.users.Load())
}
