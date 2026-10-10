package importer

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/xuri/excelize/v2"
)

func TestXLSXOversizedMiniFATIsRejected(t *testing.T) {
	data := make([]byte, 3*512)
	copy(data, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1})
	binary.LittleEndian.PutUint16(data[26:28], 3)
	binary.LittleEndian.PutUint16(data[30:32], 9)
	binary.LittleEndian.PutUint32(data[44:48], 125_000)
	binary.LittleEndian.PutUint32(data[60:64], 2)
	binary.LittleEndian.PutUint32(data[64:68], 1_000_000)
	for offset := 76; offset < 512; offset += 4 {
		binary.LittleEndian.PutUint32(data[offset:offset+4], 0xffffffff)
	}
	binary.LittleEndian.PutUint32(data[76:80], 1)
	binary.LittleEndian.PutUint32(data[512+116:512+120], 2)
	binary.LittleEndian.PutUint32(data[1024:1028], 0xfffffffe)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := Parse(bytes.NewReader(data), FormatXLSX, ""); err == nil {
		t.Fatal("Parse() error = nil")
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 2<<20 {
		t.Fatalf("Parse() allocated %d bytes", allocated)
	}
}

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

func TestXLSXDiscoveryAndSelectionContracts(t *testing.T) {
	valid := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{{"name", "address"}, {"Jane", "1 Main St"}})
		if _, err := f.NewSheet("Other"); err != nil {
			t.Fatal(err)
		}
	})
	malformedOther := rewriteXLSXEntry(t, valid, "xl/worksheets/sheet2.xml", func([]byte) []byte {
		return []byte(`<worksheet><sheetData><row r="1"><c r="invalid"><v>broken</v></c></row></sheetData></worksheet>`)
	})
	if _, err := Sheets(bytes.NewReader(malformedOther)); err == nil || !strings.Contains(err.Error(), `inspect worksheet "Other"`) {
		t.Fatalf("Sheets() error = %v, want Other inspection failure", err)
	}
	if _, err := Parse(bytes.NewReader(malformedOther), FormatXLSX, ""); err == nil || !strings.Contains(err.Error(), `inspect worksheet "Other"`) {
		t.Fatalf("Parse() error = %v, want Other inspection failure", err)
	}
	grid, err := Parse(bytes.NewReader(malformedOther), FormatXLSX, " Sheet1 ")
	if err != nil || grid == nil || grid.Len() != 1 {
		t.Fatalf("explicit Parse() grid=%v err=%v", grid, err)
	}
	if _, err := Parse(bytes.NewReader(valid), FormatXLSX, "Missing"); err == nil || err.Error() != `worksheet "Missing" does not exist` {
		t.Fatalf("missing worksheet error = %v", err)
	}

	empty := makeXLSX(t, func(*excelize.File) {})
	names, err := Sheets(bytes.NewReader(empty))
	if err != nil || names != nil {
		t.Fatalf("empty Sheets() names=%v err=%v", names, err)
	}
	for _, test := range []struct{ sheet, want string }{
		{"", "XLSX file has no non-empty worksheets"},
		{"Sheet1", "XLSX worksheet has no visible header row"},
	} {
		if _, err := Parse(bytes.NewReader(empty), FormatXLSX, test.sheet); err == nil || err.Error() != test.want {
			t.Fatalf("empty Parse(%q) error = %v, want %q", test.sheet, err, test.want)
		}
	}
}

func TestXLSXDiscoverySkipsHiddenOnlySheet(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{{"name", "address"}, {"Jane", "1 Main St"}})
		if _, err := f.NewSheet("Hidden rows"); err != nil {
			t.Fatal(err)
		}
		setRows(t, f, "Hidden rows", [][]any{{"name", "address"}, {"John", "2 Main St"}})
		for row := 1; row <= 2; row++ {
			if err := f.SetRowVisible("Hidden rows", row, false); err != nil {
				t.Fatal(err)
			}
		}
	})
	names, err := Sheets(bytes.NewReader(data))
	if err != nil || !slices.Equal(names, []string{"Sheet1"}) {
		t.Fatalf("Sheets() names=%v err=%v", names, err)
	}
	grid, err := Parse(bytes.NewReader(data), FormatXLSX, "")
	if err != nil || grid == nil || grid.Len() != 1 {
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
			{"name", "address", "capacity"},
			{"Jane", "1 Main St", 4},
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
	row := Validate(grid, AutoMap(grid.Headers), KindDriver, nil)[0]
	if !hasMessage(row.Warnings, "value comes from a formula; verify") {
		t.Fatalf("warnings = %#v", row.Warnings)
	}
	if !hasMessage(row.Errors, "whole number") {
		t.Fatalf("row = %#v, want invalid capacity rejection", row)
	}
}

func TestXLSXFormulaMetadataFailureDoesNotAbortParsing(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{{"name", "address"}, {"Jane", "1 Main St"}})
	})
	f, _, err := openXLSX(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("openXLSX() error = %v", err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	grid, err := parseXLSXSheet(f, []byte("unresolvable formula metadata"), "Sheet1")
	if err != nil {
		t.Fatalf("parseXLSXSheet() error = %v", err)
	}
	if !hasMessage(grid.Warnings, formulaMetadataWarning) {
		t.Fatalf("file warnings = %#v", grid.Warnings)
	}
}

func TestXLSXFormulaBackedHeaderWarnsAtFileLevel(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{{"name", "address"}, {"Jane", "1 Main St"}})
	})
	data = patchXLSXCell(t, data, "xl/worksheets/sheet1.xml", "A1", `<c r="A1" t="str"><f>&quot;name&quot;</f><v>name</v></c>`)
	grid, err := Parse(bytes.NewReader(data), FormatXLSX, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !hasMessage(grid.Warnings, formulaMetadataWarning) {
		t.Fatalf("file warnings = %#v", grid.Warnings)
	}
}

func TestXLSXRejectsErrorCellsInMappedColumns(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{
			{"name", "address", "location name"},
			{"Jane", "1 Main St", "Home"},
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
}

func TestXLSXDriverCapacityAcceptsIntegralFloatOnly(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{
			{"name", "address", "capacity"},
			{"Integral", "1 Main St", 4},
			{"Fractional", "2 Main St", 4.5},
		})
	})
	data = patchXLSXCellValue(t, data, "xl/worksheets/sheet1.xml", "C2", "4.0")

	grid, err := Parse(bytes.NewReader(data), FormatXLSX, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	rows := Validate(grid, AutoMap(grid.Headers), KindDriver, nil)
	if rows[0].Capacity != 4 || len(rows[0].Errors) != 0 {
		t.Fatalf("integral float row = %#v, want capacity 4 without errors", rows[0])
	}
	if !hasMessage(rows[1].Errors, "capacity must be a whole number") {
		t.Fatalf("fractional row errors = %#v, want whole-number error", rows[1].Errors)
	}
}

func TestXLSXFormulaMetadataInflatedLimitDegradesToWarning(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{{"name", "address"}, {"Jane", "1 Main St"}})
	})
	f, _, err := openXLSX(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("openXLSX() error = %v", err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	oversized := rewriteXLSXEntry(t, data, "xl/worksheets/sheet1.xml", func(contents []byte) []byte {
		padding := bytes.Repeat([]byte(" "), int(MaxFormulaMetadataXMLBytes)+1)
		return bytes.Replace(contents, []byte("</worksheet>"), append(padding, []byte("</worksheet>")...), 1)
	})
	grid, err := parseXLSXSheet(f, oversized, "Sheet1")
	if err != nil {
		t.Fatalf("parseXLSXSheet() error = %v", err)
	}
	if !hasMessage(grid.Warnings, formulaMetadataWarning) {
		t.Fatalf("file warnings = %#v, want formula metadata warning", grid.Warnings)
	}
}

func TestXLSXErrorTextIsCheckedOnlyInMappedColumns(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{
			{"name", "address", "notes"},
			{"Jane", "1 Main St", "#N/A"},
		})
	})
	grid, err := Parse(bytes.NewReader(data), FormatXLSX, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	mapping := AutoMap(grid.Headers)
	row := Validate(grid, mapping, KindParticipant, nil)[0]
	if hasMessage(row.Errors, "spreadsheet error #N/A") {
		t.Fatalf("ignored notes column errors = %#v", row.Errors)
	}

	mapping.AddressNameColumn = 2
	row = Validate(grid, mapping, KindParticipant, nil)[0]
	if !hasMessage(row.Errors, "cell C2 contains spreadsheet error #N/A") {
		t.Fatalf("mapped notes column errors = %#v", row.Errors)
	}
}

func TestXLSXNormalizesRaggedWidths(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{{"name", "address"}, {"Jane", "1 Main St"}})
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

func TestXLSXSkipsBlankAndSyntheticRows(t *testing.T) {
	t.Run("blank separator", func(t *testing.T) {
		data := makeXLSX(t, func(f *excelize.File) {
			setRows(t, f, "Sheet1", [][]any{
				{"name", "address"},
				{"Jane", "1 Main St"},
				{" ", "\t"},
				{"John", "2 Main St"},
			})
		})
		grid, err := Parse(bytes.NewReader(data), FormatXLSX, "")
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		rows := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)
		if len(rows) != 2 || rows[0].SourceRow != 2 || rows[1].SourceRow != 4 {
			t.Fatalf("rows = %#v, want source rows 2 and 4", rows)
		}
	})

	t.Run("large row gap", func(t *testing.T) {
		data := makeXLSX(t, func(f *excelize.File) {
			setRows(t, f, "Sheet1", [][]any{{"name", "address"}, {"Jane", "1 Main St"}})
			if err := f.SetCellValue("Sheet1", "A2500", "Stray"); err != nil {
				t.Fatalf("SetCellValue() error = %v", err)
			}
		})
		grid, err := Parse(bytes.NewReader(data), FormatXLSX, "")
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		rows := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)
		if len(rows) != 2 {
			t.Fatalf("len(rows) = %d, want 2", len(rows))
		}
		if rows[1].SourceRow != 2500 || rows[1].Name != "Stray" || !hasMessage(rows[1].Errors, "address is required") {
			t.Fatalf("stray row = %#v, want row-level address error at source row 2500", rows[1])
		}
	})
}

func TestXLSXParseWithManyMergedCellsCompletesQuickly(t *testing.T) {
	const dataRows = MaxDataRows / 2
	data := makeXLSX(t, func(f *excelize.File) {
		header := make([]any, MaxColumns)
		for column := range header {
			header[column] = fmt.Sprintf("column%d", column)
		}
		setRows(t, f, "Sheet1", [][]any{header})
		for row := 2; row <= dataRows+1; row++ {
			values := make([]any, MaxColumns)
			for column := range values {
				values[column] = fmt.Sprintf("r%dc%d", row, column)
			}
			cell, err := excelize.CoordinatesToCellName(1, row)
			if err != nil {
				t.Fatalf("CoordinatesToCellName() error = %v", err)
			}
			if err := f.SetSheetRow("Sheet1", cell, &values); err != nil {
				t.Fatalf("SetSheetRow() error = %v", err)
			}
		}
	})
	data = addXLSXMergedCells(t, data, "xl/worksheets/sheet1.xml", 10_000)

	started := time.Now()
	grid, err := Parse(bytes.NewReader(data), FormatXLSX, "")
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if grid.Len() != dataRows {
		t.Fatalf("Len() = %d, want %d", grid.Len(), dataRows)
	}
	if elapsed >= 60*time.Second {
		t.Fatalf("Parse() took %s, want under 60s", elapsed)
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
		return pattern.ReplaceAll(contents, fmt.Appendf(nil, "${1}%s${2}", value))
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

func addXLSXMergedCells(t *testing.T, data []byte, entryName string, count int) []byte {
	t.Helper()
	var merges strings.Builder
	fmt.Fprintf(&merges, `<mergeCells count="%d">`, count)
	for i := range count {
		row := MaxDataRows + 1000 + i
		fmt.Fprintf(&merges, `<mergeCell ref="A%d:B%d"/>`, row, row)
	}
	merges.WriteString(`</mergeCells>`)
	return rewriteXLSXEntry(t, data, entryName, func(contents []byte) []byte {
		return bytes.Replace(contents, []byte(`</worksheet>`), []byte(merges.String()+`</worksheet>`), 1)
	})
}

func TestXLSXResourceLimits(t *testing.T) {
	for _, test := range []struct {
		name string
		rows [][]any
		want string
	}{
		{"columns", [][]any{make([]any, MaxColumns+1), {"Jane"}}, fmt.Sprintf("file exceeds the limit of %d columns", MaxColumns)},
		{"cell characters", [][]any{{"name", "address"}, {strings.Repeat("a", MaxCellCharacters+1), "1 Main St"}}, fmt.Sprintf("row 2: cell 1 exceeds the limit of %d characters", MaxCellCharacters)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "columns" {
				for i := range test.rows[0] {
					test.rows[0][i] = "header"
				}
			}
			data := makeXLSX(t, func(f *excelize.File) { setRows(t, f, "Sheet1", test.rows) })
			if _, err := Parse(bytes.NewReader(data), FormatXLSX, ""); err == nil || err.Error() != test.want {
				t.Fatalf("Parse() error = %v, want %q", err, test.want)
			}
		})
	}
	t.Run("data rows", func(t *testing.T) {
		rows := [][]any{{"name", "address"}}
		for range MaxDataRows + 1 {
			rows = append(rows, []any{"Jane", "1 Main St"})
		}
		data := makeXLSX(t, func(f *excelize.File) { setRows(t, f, "Sheet1", rows) })
		want := fmt.Sprintf("file exceeds the limit of %d data rows", MaxDataRows)
		if _, err := Parse(bytes.NewReader(data), FormatXLSX, ""); err == nil || err.Error() != want {
			t.Fatalf("Parse() error = %v, want %q", err, want)
		}
	})
}

func TestXLSXParseWithChoices(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		setRows(t, f, "Sheet1", [][]any{{"name", "address"}, {"Jane", "1 Main St"}})
		if _, err := f.NewSheet("Drivers"); err != nil {
			t.Fatal(err)
		}
		setRows(t, f, "Drivers", [][]any{{"name", "address"}, {"John", "2 Main St"}})
	})
	grid, err := ParseWithChoices(bytes.NewReader(data), FormatXLSX, "")
	choice, ok := errors.AsType[*WorksheetRequiredError](err)
	if grid != nil || !ok || !slices.Equal(choice.Sheets, []string{"Sheet1", "Drivers"}) || err.Error() != "XLSX file has multiple non-empty worksheets; choose a worksheet explicitly" {
		t.Fatalf("ParseWithChoices() grid=%v err=%v", grid, err)
	}
	legacyGrid, legacyErr := Parse(bytes.NewReader(data), FormatXLSX, "")
	if legacyGrid != nil || legacyErr == nil || legacyErr.Error() != err.Error() {
		t.Fatalf("legacy Parse() grid=%v err=%v", legacyGrid, legacyErr)
	}
}

func TestParseWithChoicesErrorPresentation(t *testing.T) {
	empty := makeXLSX(t, func(*excelize.File) {})
	headerOnly := makeXLSX(t, func(f *excelize.File) { setRows(t, f, "Sheet1", [][]any{{"name", "address"}}) })
	malformed := makeXLSX(t, func(f *excelize.File) { setRows(t, f, "Sheet1", [][]any{{"name", "address"}}) })
	malformed = rewriteXLSXEntry(t, malformed, "xl/worksheets/sheet1.xml", func([]byte) []byte {
		return []byte(`<worksheet><sheetData><row r="1"><c r="invalid"><v>broken</v></c></row></sheetData></worksheet>`)
	})
	for _, test := range []struct {
		name      string
		data      []byte
		sheet     string
		discovery bool
	}{
		{"ZIP discovery", []byte("not a ZIP"), "", true},
		{"ZIP whitespace discovery", []byte("not a ZIP"), " \t", true},
		{"ZIP chosen", []byte("not a ZIP"), "Sheet1", false},
		{"worksheet discovery", malformed, "", true},
		{"worksheet chosen", malformed, "Sheet1", false},
		{"empty", empty, "", false},
		{"header only", headerOnly, "", false},
		{"missing", headerOnly, "Missing", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			grid, err := ParseWithChoices(bytes.NewReader(test.data), FormatXLSX, test.sheet)
			_, discovery := errors.AsType[*WorkbookDiscoveryError](err)
			if grid != nil || err == nil || discovery != test.discovery {
				t.Fatalf("grid=%v err=%v discovery=%t, want %t", grid, err, discovery, test.discovery)
			}
			_, legacyErr := Parse(bytes.NewReader(test.data), FormatXLSX, test.sheet)
			if legacyErr == nil || legacyErr.Error() != err.Error() {
				t.Fatalf("error=%v legacy error=%v", err, legacyErr)
			}
		})
	}
	readErr := errors.New("reader failed")
	_, err := ParseWithChoices(iotest.ErrReader(readErr), FormatXLSX, "")
	if !errors.Is(err, readErr) {
		t.Fatalf("error %v lost reader identity", err)
	}
	for _, test := range []struct {
		reader      io.Reader
		format      Format
		sheet, want string
	}{
		{nil, FormatXLSX, "", "roster file is empty"},
		{strings.NewReader("name,address\nJane,1 Main St\n"), FormatCSV, "Sheet1", "CSV files do not contain worksheets"},
		{strings.NewReader(""), Format("other"), "", `unsupported roster format "other"`},
	} {
		_, err := ParseWithChoices(test.reader, test.format, test.sheet)
		_, discovery := errors.AsType[*WorkbookDiscoveryError](err)
		if err == nil || err.Error() != test.want || discovery {
			t.Fatalf("error=%v discovery=%t want=%q", err, discovery, test.want)
		}
	}
	grid, err := ParseWithChoices(strings.NewReader("name,address\nJane,1 Main St\n"), FormatCSV, "")
	if err != nil || grid == nil || grid.Len() != 1 {
		t.Fatalf("CSV grid=%v err=%v", grid, err)
	}
}

func TestParseWithChoicesAutoSelectedFormulaSheet(t *testing.T) {
	data := makeXLSX(t, func(f *excelize.File) {
		if _, err := f.NewSheet("Roster"); err != nil {
			t.Fatal(err)
		}
		setRows(t, f, "Roster", [][]any{{"name", "address"}, {"Jane", "1 Main St"}})
		if err := f.SetCellFormula("Roster", "A2", `"Jane"`); err != nil {
			t.Fatal(err)
		}
	})
	data = patchXLSXCell(t, data, "xl/worksheets/sheet2.xml", "A2", `<c r="A2" t="str"><f>&quot;Jane&quot;</f><v>Jane</v></c>`)
	grid, err := ParseWithChoices(bytes.NewReader(data), FormatXLSX, "")
	if err != nil {
		t.Fatal(err)
	}
	row := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)[0]
	if row.Name != "Jane" || !hasMessage(row.Warnings, "value comes from a formula; verify") {
		t.Fatalf("row=%#v", row)
	}
}
