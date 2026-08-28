package logutil

import "strings"

// SafeString escapes line breaks in untrusted log values.
func SafeString(s string) string {
	return strings.NewReplacer("\n", `\n`, "\r", `\r`).Replace(s)
}
