package access_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/access"
	"ride-home-router/internal/access/accesstest"
	"strings"
	"testing"
)

type listFailureApprovals struct{ approvals }

func (s *listFailureApprovals) RecordAdminEmails(context.Context, []string) error { return nil }

func TestAccessPanelFailureRendersRetryAtServiceUnavailable(t *testing.T) {
	fixture := accesstest.New(t)
	token := fixture.Admin()
	gate, err := access.New(fixture.Config(), &listFailureApprovals{approvals{err: errors.New("private database failure")}})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gate.Register(mux)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/access", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	gate.Protect(mux).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
	for _, want := range []string{`id="access-management"`, `role="alert"`, "Could not load approved emails.", `hx-get="/api/v1/access"`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("missing %q in %s", want, response.Body.String())
		}
	}
	if response.Header().Get("X-RHR-Access-Panel") != "error" || response.Header().Get("HX-Reswap") == "none" {
		t.Fatal("error panel cannot replace loading placeholder")
	}
	if strings.Contains(response.Body.String(), "private database") {
		t.Fatal("private error leaked")
	}
}

func TestSignInHasAccessibleStatusAndExistingAccountSwitch(t *testing.T) {
	fixture := accesstest.New(t)
	gate, err := access.New(fixture.Config(), &approvals{})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gate.Register(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sign-in?denied=1", nil))
	for _, want := range []string{`id="auth-status"`, `role="status"`, "Use another account"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestAdminsCardIsReadOnlyAndAdminOnly(t *testing.T) {
	f := accesstest.New(t)
	admin := f.Admin()
	f.User("member", []string{"member@example.test"}, nil)
	f.Session("member-session", "member", "active")
	gate, err := access.New(f.Config("second@example.test", "admin@example.test"), &approvals{allowed: true})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gate.Register(mux)
	for _, tt := range []struct {
		token  string
		status int
	}{
		{admin, http.StatusOK},
		{f.Token("member", "member-session"), http.StatusForbidden},
		{"", http.StatusUnauthorized},
	} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/access/admins", nil)
		req.Header.Set("Authorization", "Bearer "+tt.token)
		gate.Protect(mux).ServeHTTP(rr, req)
		if rr.Code != tt.status {
			t.Fatalf("status %d, want %d", rr.Code, tt.status)
		}
		body := rr.Body.String()
		if tt.status == http.StatusOK {
			if !strings.Contains(body, "admin@example.test") || !strings.Contains(body, "second@example.test") || strings.Contains(body, "<form") {
				t.Fatal("missing configured admins or unexpected editing controls")
			}
		} else if strings.Contains(body, "second@example.test") {
			t.Fatal("admin email disclosed to non-admin")
		}
	}
}
