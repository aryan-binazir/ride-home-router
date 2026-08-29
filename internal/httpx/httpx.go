package httpx

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

var loopbackHostnames = [...]string{"localhost", "127.0.0.1", "::1"}

var ErrJSONContentTypeRequired = errors.New("Content-Type must be application/json")

const (
	HeaderContentType  = "Content-Type"
	HeaderHXCurrentURL = "HX-Current-URL"
	HeaderHXRequest    = "HX-Request"
	HeaderHXReswap     = "HX-Reswap"
	HeaderHXTarget     = "HX-Target"
	HeaderHXTrigger    = "HX-Trigger"

	MediaTypeJSON      = "application/json"
	MediaTypeHTML      = "text/html; charset=utf-8"
	MediaTypeForm      = "application/x-www-form-urlencoded"
	MediaTypeMultipart = "multipart/form-data"

	HTMXTrue   = "true"
	ReswapNone = "none"
)

func IsHTMX(r *http.Request) bool {
	return r.Header.Get(HeaderHXRequest) == HTMXTrue
}

func HasFormContentType(contentType string) bool {
	return HasMediaType(contentType, MediaTypeForm) || HasMediaType(contentType, MediaTypeMultipart)
}

func HasMediaType(contentType, expected string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == expected
}

func DecodeJSON(r *http.Request, dst any) error {
	if !HasMediaType(r.Header.Get(HeaderContentType), MediaTypeJSON) {
		return ErrJSONContentTypeRequired
	}
	return json.NewDecoder(r.Body).Decode(dst)
}

// LoopbackHostnames returns the hostnames accepted for local-only requests.
func LoopbackHostnames() []string {
	return slices.Clone(loopbackHostnames[:])
}

// HasSameOrigin requires Origin's host to match Host when Origin is present.
// It accepts HTTP and HTTPS for TLS-terminating proxies.
func HasSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
