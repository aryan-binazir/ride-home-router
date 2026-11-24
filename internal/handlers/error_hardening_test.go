package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aryan-binazir/ride-home-router/internal/database"
	"github.com/aryan-binazir/ride-home-router/internal/models"
)

const internalSentinel = `ERROR: relation "x" does not exist (SQLSTATE 42P01)`

type errorStore struct{ database.DataStore }

func (s errorStore) Settings() database.SettingsRepository { return errorSettings{} }

type errorSettings struct{ database.SettingsRepository }

func (errorSettings) Get(context.Context) (*models.Settings, error) {
	return nil, errors.New(internalSentinel)
}

func (errorSettings) Patch(context.Context, database.SettingsPatch) (*models.Settings, error) {
	return nil, errors.New(internalSentinel)
}

func assertSafeFailure(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(w.Body.String(), "SQLSTATE") || strings.Contains(w.Header().Get("HX-Trigger"), "SQLSTATE") || strings.Contains(w.Header().Get("Location"), "SQLSTATE") {
		t.Fatalf("leaked internal error: %s %s", w.Header(), w.Body.String())
	}
}

func TestSafeErrorResponses(t *testing.T) {
	h := &Handler{DB: errorStore{}, Renderer: loadEmbeddedTemplates(t), Geocoder: stubGeocoder{err: errors.New(internalSentinel)}}
	for _, tc := range []struct {
		name string
		fn   http.HandlerFunc
	}{
		{"settings", h.HandleUpdateSettings},
		{"desktop calculate", func(w http.ResponseWriter, r *http.Request) {
			h.handleRouteCalculationError(w, r, errors.New(internalSentinel))
		}},
		{"geocoding", func(w http.ResponseWriter, r *http.Request) {
			h.handleGeocodingError(w, r, errors.New(internalSentinel))
		}},
		{"location", h.HandleCreateActivityLocation},
		{"address search", h.HandleAddressSearch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/test?address=sample", strings.NewReader(url.Values{"name": {"Name"}, "address": {"Address"}}.Encode()))
			r.Header.Set("HX-Request", "true")
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			tc.fn(w, r)
			assertSafeFailure(t, w)
		})
	}
}

func TestEmptyParticipantAddressShowsValidationToast(t *testing.T) {
	h := &Handler{}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/participants", strings.NewReader("name=Name&address="))
	r.Header.Set("HX-Request", "true")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleCreateParticipant(w, r)
	if w.Code != 400 || !strings.Contains(w.Header().Get("HX-Trigger"), "Enter a name and address.") {
		t.Fatalf("response: %d %s", w.Code, w.Header())
	}
}

func TestImportAdmissionRejectsBeforeReadingBody(t *testing.T) {
	h, _ := newImportTestHandler(t, &importTestGeocoder{})
	for range cap(importAdmission) {
		importAdmission <- struct{}{}
	}
	defer func() {
		for range cap(importAdmission) {
			<-importAdmission
		}
	}()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/imports", unreadImportBody{t})
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandleCreateImport(w, r)
	if w.Code != 429 || !strings.Contains(w.Header().Get("HX-Trigger"), "showToast") {
		t.Fatalf("response: %d %s %s", w.Code, w.Header(), w.Body.String())
	}
}

type unreadImportBody struct{ t *testing.T }

func (b unreadImportBody) Read([]byte) (int, error) {
	b.t.Fatal("rejected import body was read")
	return 0, errors.New("unexpected read")
}
