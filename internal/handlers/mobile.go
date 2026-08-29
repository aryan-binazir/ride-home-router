package handlers

import (
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"net/url"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/logutil"
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/routesession"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	mobileDraftCookie                = "rhr_mobile_draft"
	mobileDraftCookieMaxAge          = 8 * time.Hour
	messageMobileInvalidForm         = "Check the form and try again."
	messageMobileAddressLookupFailed = "Could not find that address. Check it and try again."
)

func (h *Handler) mobileDraft(w http.ResponseWriter, r *http.Request) (string, plandraft.Draft, string) {
	if cookie, err := r.Cookie(mobileDraftCookie); err == nil && validMobileDraftID(cookie.Value) {
		if draft, ok := h.PlanDraft.Get(cookie.Value); ok {
			h.setMobileDraftCookie(w, r, cookie.Value)
			return cookie.Value, draft, ""
		}
		id, draft := h.newMobileDraft(w, r)
		return id, draft, "Your saved plan expired. Start a new plan below."
	}
	id, draft := h.newMobileDraft(w, r)
	return id, draft, ""
}

func (h *Handler) newMobileDraft(w http.ResponseWriter, r *http.Request) (string, plandraft.Draft) {
	id := h.PlanDraft.NewID()
	draft := h.PlanDraft.Update(id, func(*plandraft.Draft) {})
	h.setMobileDraftCookie(w, r, id)
	return id, draft
}

func (h *Handler) setMobileDraftCookie(w http.ResponseWriter, r *http.Request, id string) {
	//nolint:gosec // Local HTTP is supported; this cookie contains only an opaque random identifier.
	http.SetCookie(w, &http.Cookie{
		Name: mobileDraftCookie, Value: id, Path: "/", Secure: r.TLS != nil, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(mobileDraftCookieMaxAge.Seconds()),
	})
}

func validMobileDraftID(id string) bool {
	if len(id) != 32 || id != strings.ToLower(id) {
		return false
	}
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 16
}

func mobileSelected(ids []int64) map[int64]bool {
	selected := make(map[int64]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	return selected
}

func parseMobileIDs(values []string) []int64 {
	seen := make(map[int64]bool, len(values))
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err == nil && id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func mobileID(path, prefix, suffix string) (int64, error) {
	value := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	value = strings.Trim(value, "/")
	if value == "" || strings.Contains(value, "/") {
		return 0, errors.New("invalid id")
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

func (h *Handler) renderMobileError(w http.ResponseWriter, r *http.Request, status int, message string, err error) {
	if err != nil {
		log.Printf("[ERROR] Mobile request failed: method=%s path=%s err=%v", logutil.SafeString(r.Method), logutil.SafeString(r.URL.Path), err)
	}
	w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
	w.WriteHeader(status)
	view := mobileErrorView{mobileBaseView: newMobileBase(http.StatusText(status), mobileActiveTab(r.URL.Path), ""), Message: message}
	if renderErr := h.Renderer.Render(w, "mobile/error.html", view); renderErr != nil {
		log.Printf("[ERROR] Mobile error template failed: path=%s err=%v", logutil.SafeString(r.URL.Path), renderErr)
	}
}

func (h *Handler) renderMobileTemplateStatus(w http.ResponseWriter, r *http.Request, status int, name string, view any) {
	w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
	w.WriteHeader(status)
	if err := h.Renderer.Render(w, name, view); err != nil {
		log.Printf("[ERROR] Mobile template failed: path=%s template=%s err=%v", logutil.SafeString(r.URL.Path), name, err)
	}
}

func (h *Handler) renderMobileStoreError(w http.ResponseWriter, r *http.Request, err error, notFoundMessage string) {
	if h.checkNotFound(err) {
		h.renderMobileError(w, r, http.StatusNotFound, notFoundMessage, err)
		return
	}
	h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, err)
}

func mobileActiveTab(path string) string {
	switch {
	case strings.HasPrefix(path, "/m/people"):
		return "people"
	case strings.HasPrefix(path, "/m/places"):
		return "places"
	case strings.HasPrefix(path, "/m/history"):
		return "history"
	default:
		return "plan"
	}
}

func logMobileRequest(r *http.Request) {
	log.Printf("[HTTP] %s %s", logutil.SafeString(r.Method), logutil.SafeString(r.URL.Path))
}

func (h *Handler) mobileRedirectError(w http.ResponseWriter, r *http.Request, path, message string) {
	if !strings.HasPrefix(path, "/m") {
		path = "/m"
	}
	target, err := url.Parse(path)
	if err != nil || target == nil {
		target = &url.URL{Path: "/m"}
	}
	query := target.Query()
	query.Set("error", message)
	target.RawQuery = query.Encode()
	//nolint:gosec // The prefix check restricts redirects to the same-origin mobile application.
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}

func mobileRouteErrorMessage(err error) string {
	switch {
	case errors.Is(err, routesession.ErrNotFound), errors.Is(err, routesession.ErrAlreadyCommitted):
		return "That route plan expired. Calculate it again."
	case errors.Is(err, routesession.ErrInvalidRouteIndex):
		return messageInvalidRouteIndex
	case errors.Is(err, routesession.ErrParticipantNotFound), errors.Is(err, routesession.ErrParticipantNotInSource):
		return messageParticipantNotFound
	case errors.Is(err, routesession.ErrSwapMissingDriver):
		return "Both routes need drivers before they can be swapped."
	case errors.Is(err, routesession.ErrSwapCapacity):
		return "Those drivers cannot be swapped because a route would exceed capacity."
	case errors.Is(err, routesession.ErrDriverNotSelected), errors.Is(err, routesession.ErrDriverAlreadyInRoutes):
		return "Choose an unused driver."
	case errors.Is(err, routesession.ErrUnbalanced):
		return messageRoutesMustBeBalancedBeforeSaving
	default:
		return messageGenericInternalError
	}
}

func filterByLabel[T any](items []T, ids func(T) int64, memberships map[int64][]int64, labelID int64) []T {
	if labelID == 0 {
		return items
	}
	result := make([]T, 0, len(items))
	for _, item := range items {
		if slices.Contains(memberships[ids(item)], labelID) {
			result = append(result, item)
		}
	}
	return result
}

func formatMobileTime(value string) string {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return value
	}
	return parsed.Format("3:04 PM")
}
