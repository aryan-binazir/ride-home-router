package httpx

import (
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"
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

// IsLoopbackHost reports whether host is on the local-request allowlist, with
// or without a port.
func IsLoopbackHost(host string) bool {
	hostname := host
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		hostname = parsedHost
	}
	hostname = strings.Trim(hostname, "[]")
	for _, allowed := range loopbackHostnames {
		if strings.EqualFold(hostname, allowed) {
			return true
		}
	}
	return false
}

// HasSameHTTPOrigin reports whether the request has no Origin header or has an
// HTTP Origin that exactly matches its Host.
func HasSameHTTPOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || strings.EqualFold(origin, "http://"+r.Host)
}
