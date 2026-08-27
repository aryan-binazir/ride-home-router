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

// HasSameOrigin reports whether the request has no Origin header or an http(s)
// Origin whose host exactly matches the request Host. Both schemes are accepted
// because a TLS-terminating proxy or tunnel forwards https origins to this
// plain-HTTP listener.
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
