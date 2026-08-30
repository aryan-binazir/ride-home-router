package migrations

import "testing"

func TestHasExecutableSQL(t *testing.T) {
	tests := map[string]struct {
		body string
		want bool
	}{
		"empty":                 {body: "", want: false},
		"whitespace":            {body: " \n\t", want: false},
		"line comments":         {body: "-- TODO: write rollback\n-- still disabled", want: false},
		"CR line then SQL":      {body: "-- rollback\rDROP TABLE routes;", want: true},
		"CRLF line then SQL":    {body: "-- rollback\r\nDROP TABLE routes;", want: true},
		"block comment":         {body: "/* TODO: write rollback */", want: false},
		"nested block comments": {body: "/* outer /* nested */ outer */", want: false},
		"empty delimiters":      {body: "; ;\n;", want: false},
		"bom and comments":      {body: "\ufeff; /* disabled */ -- still disabled", want: false},
		"statement":             {body: "DROP TABLE routes;", want: true},
		"comment and SQL":       {body: "/* rollback */\nDROP TABLE routes;", want: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := hasExecutableSQL(test.body); got != test.want {
				t.Fatalf("hasExecutableSQL(%q) = %t, want %t", test.body, got, test.want)
			}
		})
	}
}
