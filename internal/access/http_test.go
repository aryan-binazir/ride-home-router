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
