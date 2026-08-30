package migrations

import "testing"

func TestHasExecutableSQL(t *testing.T) {
	tests := map[string]struct {
		body string
		want bool
	}{
		"empty":           {body: "", want: false},
		"whitespace":      {body: " \n\t", want: false},
		"comments":        {body: "-- TODO: write rollback\n-- still disabled", want: false},
		"statement":       {body: "DROP TABLE routes;", want: true},
		"comment and SQL": {body: "-- rollback\nDROP TABLE routes;", want: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := hasExecutableSQL(test.body); got != test.want {
				t.Fatalf("hasExecutableSQL(%q) = %t, want %t", test.body, got, test.want)
			}
		})
	}
}
