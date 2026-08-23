package importer

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseCSVStripsBOMTrimsAndReadsQuotedFields(t *testing.T) {
	grid, err := Parse(strings.NewReader("\ufeff Name , Address ,Note\n\"Doe, Jane\",\" 1 Main St \",ignored\n"), FormatCSV, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if grid.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", grid.Len())
	}
	if got, want := grid.Headers, []string{"Name", "Address", "Note"}; !equalStrings(got, want) {
		t.Fatalf("Headers = %#v, want %#v", got, want)
	}
	rows := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)
	if rows[0].Name != "Doe, Jane" || rows[0].Address != "1 Main St" {
		t.Fatalf("row = %#v", rows[0])
	}
}

func TestParseCSVRejectsInvalidUTF8(t *testing.T) {
	_, err := Parse(strings.NewReader("name,address\nJane,\xff\n"), FormatCSV, "")
	if err == nil || !strings.Contains(err.Error(), "re-save the file as a UTF-8 CSV") {
		t.Fatalf("Parse() error = %v, want UTF-8 guidance", err)
	}
}

func TestParseCSVRejectsSemicolonHeader(t *testing.T) {
	_, err := Parse(strings.NewReader("name;address\nJane;1 Main St\n"), FormatCSV, "")
	if err == nil || !strings.Contains(err.Error(), "comma-delimited") {
		t.Fatalf("Parse() error = %v, want comma-delimited guidance", err)
	}
}

func TestParseCSVRaggedRowsAreRowErrors(t *testing.T) {
	grid := mustParseCSV(t, "name,address\nJane\nJohn,2 Main St,extra\n")
	rows := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	for i := range rows {
		if !hasMessage(rows[i].Errors, "columns") {
			t.Errorf("row %d errors = %#v, want field-count error", rows[i].SourceRow, rows[i].Errors)
		}
	}
	if rows[1].Address != "2 Main St" {
		t.Fatalf("long row address = %q, want normalized mapped value", rows[1].Address)
	}
}

func TestParseCSVRequiresHeaderAndData(t *testing.T) {
	for _, test := range []struct {
		name string
		csv  string
		want string
	}{
		{name: "empty", csv: "", want: "header"},
		{name: "header only", csv: "name,address\n", want: "no data rows"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(test.csv), FormatCSV, "")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseCSVLimitConstants(t *testing.T) {
	t.Run("data rows", func(t *testing.T) {
		var csv strings.Builder
		csv.WriteString("name,address\n")
		for i := 0; i < MaxDataRows; i++ {
			fmt.Fprintf(&csv, "Person %d,%d Main St\n", i, i)
		}
		grid, err := Parse(strings.NewReader(csv.String()), FormatCSV, "")
		if err != nil || grid.Len() != MaxDataRows {
			t.Fatalf("boundary Parse() grid=%v err=%v", grid, err)
		}
		csv.WriteString("One Too Many,Elsewhere\n")
		_, err = Parse(strings.NewReader(csv.String()), FormatCSV, "")
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%d data rows", MaxDataRows)) {
			t.Fatalf("over-limit error = %v", err)
		}
	})

	t.Run("columns", func(t *testing.T) {
		header := numberedCells(MaxColumns)
		grid, err := Parse(strings.NewReader(strings.Join(header, ",")+"\n"+strings.Repeat(",", MaxColumns-1)+"\n"), FormatCSV, "")
		if err != nil || len(grid.Headers) != MaxColumns {
			t.Fatalf("boundary Parse() grid=%v err=%v", grid, err)
		}
		header = numberedCells(MaxColumns + 1)
		_, err = Parse(strings.NewReader(strings.Join(header, ",")+"\nvalue\n"), FormatCSV, "")
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%d columns", MaxColumns)) {
			t.Fatalf("over-limit error = %v", err)
		}
	})

	t.Run("cell characters", func(t *testing.T) {
		boundary := strings.Repeat("é", MaxCellCharacters)
		if _, err := Parse(strings.NewReader("name,address\n"+boundary+",Somewhere\n"), FormatCSV, ""); err != nil {
			t.Fatalf("boundary Parse() error = %v", err)
		}
		tooLong := strings.Repeat("x", MaxCellCharacters+1)
		_, err := Parse(strings.NewReader("name,address\n"+tooLong+",Somewhere\n"), FormatCSV, "")
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%d characters", MaxCellCharacters)) {
			t.Fatalf("over-limit error = %v", err)
		}
	})
}

func mustParseCSV(t *testing.T, contents string) *Grid {
	t.Helper()
	grid, err := Parse(strings.NewReader(contents), FormatCSV, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return grid
}

func numberedCells(count int) []string {
	values := make([]string, count)
	for i := range values {
		values[i] = fmt.Sprintf("column%d", i)
	}
	return values
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasMessage(messages []string, substring string) bool {
	for _, message := range messages {
		if strings.Contains(message, substring) {
			return true
		}
	}
	return false
}
