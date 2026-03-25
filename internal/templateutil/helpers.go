package templateutil

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"
)

const (
	metersPerMile      = 1609.344
	metersPerKilometer = 1000.0
)

// FuncMap returns the shared template helper functions used in production and tests.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"formatDate": func(t time.Time) string {
			return t.Format("2006-01-02")
		},
		"add": func(a, b int) int {
			return a + b
		},
		"currentDate": func() string {
			return time.Now().Format("2006-01-02")
		},
		"toJSON": func(v interface{}) string {
			b, err := json.Marshal(v)
			if err != nil {
				return "{}"
			}
			return string(b)
		},
		"toJSONRaw": func(v interface{}) template.JS {
			b, err := json.Marshal(v)
			if err != nil {
				return template.JS("[]")
			}
			return template.JS(b)
		},
		"formatDistance": func(meters float64, useMiles bool) string {
			if useMiles {
				return fmt.Sprintf("%.2f mi", meters/metersPerMile)
			}
			return fmt.Sprintf("%.2f km", meters/metersPerKilometer)
		},
		"formatDuration": func(seconds float64) string {
			mins := int(seconds / 60)
			secs := int(seconds) % 60
			if mins == 0 {
				return fmt.Sprintf("%ds", secs)
			}
			if secs == 0 {
				return fmt.Sprintf("%dm", mins)
			}
			return fmt.Sprintf("%dm %ds", mins, secs)
		},
		"initials": func(name string) string {
			parts := strings.Fields(strings.TrimSpace(name))
			if len(parts) == 0 {
				return ""
			}

			first := []rune(parts[0])
			if len(parts) == 1 {
				if len(first) == 0 {
					return ""
				}
				return strings.ToUpper(string(first[0]))
			}

			last := []rune(parts[len(parts)-1])
			if len(first) == 0 || len(last) == 0 {
				return ""
			}
			return strings.ToUpper(string(first[0]) + string(last[0]))
		},
		"dict": func(keyvals ...any) map[string]any {
			m := make(map[string]any, len(keyvals)/2)
			for i := 0; i+1 < len(keyvals); i += 2 {
				if key, ok := keyvals[i].(string); ok {
					m[key] = keyvals[i+1]
				}
			}
			return m
		},
		"groupIDsFor": func(m map[int64][]int64, id int64) string {
			values := m[id]
			if len(values) == 0 {
				return ""
			}
			parts := make([]string, 0, len(values))
			for _, v := range values {
				parts = append(parts, strconv.FormatInt(v, 10))
			}
			return strings.Join(parts, ",")
		},
		"joinInt64s": func(values []int64, sep string) string {
			if len(values) == 0 {
				return ""
			}
			parts := make([]string, 0, len(values))
			for _, value := range values {
				parts = append(parts, strconv.FormatInt(value, 10))
			}
			return strings.Join(parts, sep)
		},
	}
}
