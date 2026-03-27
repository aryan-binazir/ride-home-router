package httpx

import (
	"net/http"
	"strings"
)

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
	return strings.Contains(contentType, MediaTypeForm) || strings.Contains(contentType, MediaTypeMultipart)
}
