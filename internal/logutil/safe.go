package logutil

import "strings"

func SafeString(s string) string {
	return strings.NewReplacer("\n", `\n`, "\r", `\r`).Replace(s)
}
