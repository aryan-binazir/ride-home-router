package access_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aryan-binazir/ride-home-router/internal/access/accesstest"
)

type lookupRead struct {
	start, finish time.Time
}

type snapshotTransport struct {
	base                    http.RoundTripper
	sessionDelay, userDelay time.Duration
	mu                      sync.Mutex
	sessions, users         []lookupRead
}

func (s *snapshotTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	start := time.Now()
	response, err := s.base.RoundTrip(r)
	session := strings.HasPrefix(r.URL.Path, "/v1/sessions/")
	delay := s.userDelay
	if session {
		delay = s.sessionDelay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-r.Context().Done():
		if response != nil {
			_ = response.Body.Close()
		}
		response, err = nil, r.Context().Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	read := lookupRead{start, time.Now()}
	if session {
		s.sessions = append(s.sessions, read)
	} else {
		s.users = append(s.users, read)
	}
	return response, err
}

func TestClerkAcceptanceWindowFromLookupStart(t *testing.T) {
	for _, delays := range []struct{ session, user time.Duration }{
		{100 * time.Millisecond, 100 * time.Millisecond},
		{200 * time.Millisecond, 100 * time.Millisecond},
		{100 * time.Millisecond, 200 * time.Millisecond},
	} {
		for _, change := range []string{"deleted", "unverified", "banned", "locked", "revoked"} {
			for _, at := range []time.Duration{-time.Nanosecond, time.Nanosecond, 25 * time.Millisecond, 75 * time.Millisecond, 125 * time.Millisecond, 175 * time.Millisecond, 225 * time.Millisecond} {
				t.Run(fmt.Sprintf("session=%s/user=%s/%s/at=%s", delays.session, delays.user, change, at), func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						f := accesstest.New(t)
						token := f.Admin()
						mutate := func() {
							switch change {
							case "deleted":
								f.Fail("/v1/users/user_admin", http.StatusNotFound)
								f.Session("sess_admin", "user_admin", "revoked")
							case "unverified":
								f.User("user_admin", nil, []string{"admin@example.test"})
							case "banned", "locked":
								f.User("user_admin", []string{"admin@example.test"}, nil, map[string]any{change: true})
							case "revoked":
								f.Session("sess_admin", "user_admin", "revoked")
							}
						}
						cfg := f.Config()
						transport := &snapshotTransport{base: cfg.HTTPClient.Transport, sessionDelay: delays.session, userDelay: delays.user}
						cfg.HTTPClient.Transport = transport
						h := evidenceHandler(t, cfg, false)
						if at < 0 {
							mutate()
						} else {
							go func() { time.Sleep(at); mutate() }()
						}
						first := evidenceRequest(h, token).Code
						synctest.Wait()
						if first != 204 {
							if first != 401 && first != 403 {
								t.Fatalf("first status=%d", first)
							}
							return
						}
						if at < 0 {
							t.Fatal("mutation before lookup admitted")
						}
						userRead, sessionRead := transport.users[0], transport.sessions[0]
						expires := userRead.finish.Add(30 * time.Second)
						time.Sleep(time.Until(expires) - time.Nanosecond)
						if got := evidenceRequest(h, token).Code; got != 204 {
							t.Fatalf("before anchored expiry: %d", got)
						}
						lastAccepted := time.Now()
						mainUserWindow := delays.user + 30*time.Second
						mainSessionWindow := delays.session + delays.user + 30*time.Second
						if lastAccepted.Sub(userRead.start) >= mainUserWindow || lastAccepted.Sub(sessionRead.start) >= mainSessionWindow {
							t.Fatalf("acceptance window exceeds sequential main: user=%s bound=%s session=%s bound=%s", lastAccepted.Sub(userRead.start), mainUserWindow, lastAccepted.Sub(sessionRead.start), mainSessionWindow)
						}
						time.Sleep(time.Nanosecond)
						want := 401
						if change == "unverified" {
							want = 403
						}
						if got := evidenceRequest(h, token).Code; got != want {
							t.Fatalf("at anchored expiry: %d want %d", got, want)
						}
					})
				})
			}
		}
	}
}

type cancelUserTransport struct {
	base              http.RoundTripper
	started, canceled chan struct{}
}

func (s cancelUserTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasPrefix(r.URL.Path, "/v1/users/") {
		close(s.started)
		<-r.Context().Done()
		close(s.canceled)
		return nil, r.Context().Err()
	}
	<-s.started
	return s.base.RoundTrip(r)
}

func TestRejectedSessionCancelsSpeculativeUser(t *testing.T) {
	for _, outcome := range []string{"missing", "revoked", "expired", "error", "wrong-user"} {
		t.Run(outcome, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := accesstest.New(t)
				token := f.Admin()
				switch outcome {
				case "missing":
					f.Fail("/v1/sessions/sess_admin", 404)
				case "revoked", "expired":
					f.Session("sess_admin", "user_admin", outcome)
				case "error":
					f.Fail("/v1/sessions/sess_admin", 503)
				}
				cfg := f.Config()
				transport := cancelUserTransport{cfg.HTTPClient.Transport, make(chan struct{}), make(chan struct{})}
				cfg.HTTPClient.Transport = transport
				h := evidenceHandler(t, cfg, true)
				if outcome == "wrong-user" {
					token = f.Token("other_user", "sess_admin")
				}
				start := time.Now()
				want := 401
				if outcome == "error" {
					want = 503
				}
				if got := evidenceRequest(h, token).Code; got != want {
					t.Fatalf("session precedence: %d want %d", got, want)
				}
				synctest.Wait()
				select {
				case <-transport.canceled:
				default:
					t.Fatal("speculative user was not canceled")
				}
				if elapsed := time.Since(start); elapsed != 0 {
					t.Fatalf("session denial waited for user: %s", elapsed)
				}
			})
		})
	}
}

func TestTerminalClerkDenialTTLStartsAtInsertion(t *testing.T) {
	for _, delays := range []struct{ session, user time.Duration }{
		{100 * time.Millisecond, 200 * time.Millisecond},
		{200 * time.Millisecond, 100 * time.Millisecond},
	} {
		for _, outcome := range []string{"missing-session", "ended-session", "deleted-user", "banned", "locked"} {
			t.Run(fmt.Sprintf("session=%s/user=%s/%s", delays.session, delays.user, outcome), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					f := accesstest.New(t)
					token := f.Admin()
					switch outcome {
					case "missing-session":
						f.Fail("/v1/sessions/sess_admin", 404)
					case "ended-session":
						f.Session("sess_admin", "user_admin", "ended")
					case "deleted-user":
						f.Fail("/v1/users/user_admin", 404)
					case "banned", "locked":
						f.User("user_admin", []string{"admin@example.test"}, nil, map[string]any{outcome: true})
					}
					cfg := f.Config()
					cfg.HTTPClient.Transport = &snapshotTransport{base: cfg.HTTPClient.Transport, sessionDelay: delays.session, userDelay: delays.user}
					h := evidenceHandler(t, cfg, true)
					if got := evidenceRequest(h, token).Code; got != 401 {
						t.Fatalf("initial terminal denial: %d", got)
					}
					f.Fail("/v1/sessions/sess_admin", 0)
					f.Fail("/v1/users/user_admin", 0)
					f.Session("sess_admin", "user_admin", "active")
					f.User("user_admin", []string{"admin@example.test"}, nil)
					time.Sleep(30*time.Second - time.Nanosecond)
					if got := evidenceRequest(h, token).Code; got != 401 {
						t.Fatalf("before terminal-denial expiry: %d", got)
					}
					time.Sleep(time.Nanosecond)
					if got := evidenceRequest(h, token).Code; got != 204 {
						t.Fatalf("at terminal-denial expiry: %d", got)
					}
				})
			})
		}
	}
}
