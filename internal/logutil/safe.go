package logutil

import "strings"

// SafeString strips line breaks from untrusted log values.
func SafeString(s string) string {
	return strings.NewReplacer("\n", `\n`, "\r", `\r`).Replace(s)
}
