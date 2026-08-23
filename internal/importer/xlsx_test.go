package importer

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestXLSXMultipleSheetsRequireSelection(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{{"name", "address"}, {"Jane", "1 Main St"}})
		if _, err := f.NewSheet("Drivers"); err != nil {
			t.Fatalf("NewSheet() error = %v", err)
		}
		setRows(t, f, "Drivers", [][]any{{"name", "address"}, {"John", "2 Main St"}})
		if _, err := f.NewSheet("Empty"); err != nil {
			t.Fatalf("NewSheet() error = %v", err)
		}
	})

	names, err := Sheets(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Sheets() error = %v", err)
	}
	if !equalStrings(names, []string{"Sheet1", "Drivers"}) {
		t.Fatalf("Sheets() = %#v", names)
	}
	if _, err := Parse(bytes.NewReader(data), FormatXLSX, ""); err == nil || !strings.Contains(err.Error(), "choose a worksheet") {
		t.Fatalf("Parse() error = %v", err)
	}
	grid, err := Parse(bytes.NewReader(data), FormatXLSX, "Drivers")
	if err != nil || grid.Len() != 1 {
		t.Fatalf("selected Parse() grid=%v err=%v", grid, err)
	}
}

func TestXLSXOneNonEmptySheetIsSelected(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{{"name", "address"}, {"Jane", "1 Main St"}})
		if _, err := f.NewSheet("Empty"); err != nil {
			t.Fatalf("NewSheet() error = %v", err)
		}
	})
	grid, err := Parse(bytes.NewReader(data), FormatXLSX, "")
	if err != nil || grid.Len() != 1 {
		t.Fatalf("Parse() grid=%v err=%v", grid, err)
	}
}

func TestXLSXSkipsHiddenRows(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{
			{"name", "address"},
			{"Hidden", "1 Main St"},
			{"Visible", "2 Main St"},
		})
		if err := f.SetRowVisible("Sheet1", 2, false); err != nil {
			t.Fatalf("SetRowVisible() error = %v", err)
		}
	})
	grid, err := Parse(bytes.NewReader(data), FormatXLSX, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	rows := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)
	if len(rows) != 1 || rows[0].Name != "Visible" || rows[0].SourceRow != 3 {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestXLSXUsesRawFormulaCacheAndWarns(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{
			{"name", "address", "lat", "lng"},
			{"Jane", "1 Main St", 0.0, -73.0},
		})
		if err := f.SetCellFormula("Sheet1", "C2", "0/0"); err != nil {
			t.Fatalf("SetCellFormula() error = %v", err)
		}
	})
	data = patchXLSXCellValue(t, data, "xl/worksheets/sheet1.xml", "C2", "NaN")
	grid, err := Parse(bytes.NewReader(data), FormatXLSX, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	row := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)[0]
	if !hasMessage(row.Warnings, "value comes from a formula; verify") {
		t.Fatalf("warnings = %#v", row.Warnings)
	}
	if !hasMessage(row.Errors, "not finite") {
		t.Fatalf("row = %#v, want NaN rejection", row)
	}
}

func TestXLSXUsesRawNumericValuesAndRejectsErrorCells(t *testing.T) {
	t.Run("formatted numeric value", func(t *testing.T) {
		data := makeXLSX(t, func(f *excelize.File) {
			setRows(t, f, "Sheet1", [][]any{
				{"name", "address", "lat", "lng"},
				{"Jane", "1 Main St", 0.5, -73.0},
			})
			style, err := f.NewStyle(&excelize.Style{NumFmt: 10})
			if err != nil {
				t.Fatalf("NewStyle() error = %v", err)
			}
			if err := f.SetCellStyle("Sheet1", "C2", "C2", style); err != nil {
				t.Fatalf("SetCellStyle() error = %v", err)
			}
		})
		grid, err := Parse(bytes.NewReader(data), FormatXLSX, "")
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		row := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)[0]
		if len(row.Errors) != 0 || row.Lat != 0.5 {
			t.Fatalf("row = %#v, want raw latitude 0.5", row)
		}
	})

	t.Run("spreadsheet error", func(t *testing.T) {
		data := makeXLSX(t, func(f *excelize.File) {
			setRows(t, f, "Sheet1", [][]any{
				{"name", "address", "lat", "lng"},
				{"Jane", "1 Main St", 0.0, -73.0},
			})
		})
		data = patchXLSXCell(t, data, "xl/worksheets/sheet1.xml", "C2", `<c r="C2" t="e"><v>#REF!</v></c>`)
		grid, err := Parse(bytes.NewReader(data), FormatXLSX, "")
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		row := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)[0]
		if !hasMessage(row.Errors, "spreadsheet error #REF!") {
			t.Fatalf("errors = %#v", row.Errors)
		}
	})
}

func TestXLSXNormalizesRaggedWidths(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{{"name", "address", "lat", "lng"}, {"Jane", "1 Main St"}})
	})
	grid, err := Parse(bytes.NewReader(data), FormatXLSX, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	row := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)[0]
	if !row.NeedsGeocoding || len(row.Errors) != 0 {
		t.Fatalf("row = %#v", row)
	}
}

func makeXLSX(t *testing.T, setup func(*excelize.File)) []byte {
	t.Helper()
	f := excelize.NewFile()
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	setup(f)
	buffer, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("WriteToBuffer() error = %v", err)
	}
	return buffer.Bytes()
}

func setRows(t *testing.T, f *excelize.File, sheet string, rows [][]any) {
	t.Helper()
	for i, row := range rows {
		cell, err := excelize.CoordinatesToCellName(1, i+1)
		if err != nil {
			t.Fatalf("CoordinatesToCellName() error = %v", err)
		}
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			t.Fatalf("SetSheetRow() error = %v", err)
		}
	}
}

func patchXLSXCellValue(t *testing.T, data []byte, entryName, cell, value string) []byte {
	t.Helper()
	pattern := regexp.MustCompile(`(<c[^>]*\br="` + regexp.QuoteMeta(cell) + `"[^>]*>.*?<v>)[^<]*(</v>.*?</c>)`)
	return rewriteXLSXEntry(t, data, entryName, func(contents []byte) []byte {
		return pattern.ReplaceAll(contents, []byte(fmt.Sprintf("${1}%s${2}", value)))
	})
}

func patchXLSXCell(t *testing.T, data []byte, entryName, cell, replacement string) []byte {
	t.Helper()
	pattern := regexp.MustCompile(`(?s)<c[^>]*\br="` + regexp.QuoteMeta(cell) + `"[^>]*>.*?</c>`)
	return rewriteXLSXEntry(t, data, entryName, func(contents []byte) []byte {
		return pattern.ReplaceAll(contents, []byte(replacement))
	})
}

func rewriteXLSXEntry(t *testing.T, data []byte, entryName string, rewrite func([]byte) []byte) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader() error = %v", err)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	patched := false
	for _, entry := range reader.File {
		source, err := entry.Open()
		if err != nil {
			t.Fatalf("open ZIP entry %q: %v", entry.Name, err)
		}
		contents, readErr := io.ReadAll(source)
		closeErr := source.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read ZIP entry %q: read=%v close=%v", entry.Name, readErr, closeErr)
		}
		if entry.Name == entryName {
			replaced := rewrite(contents)
			patched = !bytes.Equal(replaced, contents)
			contents = replaced
		}
		destination, err := writer.CreateHeader(&entry.FileHeader)
		if err != nil {
			t.Fatalf("create ZIP entry %q: %v", entry.Name, err)
		}
		if _, err := destination.Write(contents); err != nil {
			t.Fatalf("write ZIP entry %q: %v", entry.Name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close ZIP writer: %v", err)
	}
	if !patched {
		t.Fatalf("expected content was not found in %s", entryName)
	}
	return output.Bytes()
}
