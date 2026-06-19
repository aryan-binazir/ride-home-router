package logutil

import "strings"

// SafeString replaces newline characters so user-controlled values cannot forge log lines.
func SafeString(s string) string {
	return strings.NewReplacer("\n", `\n`, "\r", `\r`).Replace(s)
}
