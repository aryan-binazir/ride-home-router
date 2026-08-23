package importer

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

const (
	xlsxUnzipSizeLimit    = 64 << 20
	xlsxUnzipXMLSizeLimit = 8 << 20
)

var errInvalidUTF8 = errors.New("invalid UTF-8")

// Parse reads a CSV or XLSX roster into a normalized grid.
func Parse(r io.Reader, format Format, sheet string) (*Grid, error) {
	if r == nil {
		return nil, errors.New("roster file is empty")
	}
	switch format {
	case FormatCSV:
		if strings.TrimSpace(sheet) != "" {
			return nil, errors.New("CSV files do not contain worksheets")
		}
		return parseCSV(r)
	case FormatXLSX:
		return parseXLSX(r, sheet)
	default:
		return nil, fmt.Errorf("unsupported roster format %q", format)
	}
}

// Sheets returns the names of XLSX worksheets containing at least one visible
// row. The reader must contain XLSX data.
func Sheets(r io.Reader) (names []string, err error) {
	if r == nil {
		return nil, errors.New("roster file is empty")
	}
	f, err := openXLSX(r)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close XLSX file: %w", closeErr)
		}
	}()
	return nonEmptySheets(f)
}

func parseCSV(r io.Reader) (*Grid, error) {
	reader := csv.NewReader(newUTF8Reader(r))
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		if errors.Is(err, errInvalidUTF8) {
			return nil, utf8CSVError()
		}
		if errors.Is(err, io.EOF) {
			return nil, errors.New("roster file has no header row")
		}
		return nil, fmt.Errorf("read CSV header: %w", err)
	}
	if err := normalizeAndCheckCells(header); err != nil {
		return nil, fmt.Errorf("header row: %w", err)
	}
	if len(header) > MaxColumns {
		return nil, fmt.Errorf("file exceeds the limit of %d columns", MaxColumns)
	}
	if len(header) == 1 && strings.Contains(header[0], ";") {
		return nil, errors.New("CSV header appears semicolon-delimited; re-export it as a comma-delimited CSV")
	}
	if len(header) == 0 {
		return nil, errors.New("roster file has no header row")
	}

	grid := &Grid{Headers: header}
	for {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if errors.Is(readErr, errInvalidUTF8) {
			return nil, utf8CSVError()
		}
		if readErr != nil {
			return nil, fmt.Errorf("read CSV row %d: %w", len(grid.rows)+2, readErr)
		}
		if len(grid.rows) == MaxDataRows {
			return nil, fmt.Errorf("file exceeds the limit of %d data rows", MaxDataRows)
		}
		if len(record) > MaxColumns {
			return nil, fmt.Errorf("file exceeds the limit of %d columns", MaxColumns)
		}
		if err := normalizeAndCheckCells(record); err != nil {
			return nil, fmt.Errorf("row %d: %w", len(grid.rows)+2, err)
		}

		row := gridRow{sourceRow: len(grid.rows) + 2}
		if len(record) != len(header) {
			row.errors = append(row.errors, fmt.Sprintf("row has %d columns; header has %d", len(record), len(header)))
		}
		row.cells = normalizeWidth(record, len(header))
		grid.rows = append(grid.rows, row)
	}
	if len(grid.rows) == 0 {
		return nil, errors.New("roster file has no data rows after the header")
	}
	return grid, nil
}

func parseXLSX(r io.Reader, requestedSheet string) (grid *Grid, err error) {
	f, err := openXLSX(r)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close XLSX file: %w", closeErr)
			grid = nil
		}
	}()

	sheet := strings.TrimSpace(requestedSheet)
	if sheet == "" {
		names, namesErr := nonEmptySheets(f)
		if namesErr != nil {
			return nil, namesErr
		}
		switch len(names) {
		case 0:
			return nil, errors.New("XLSX file has no non-empty worksheets")
		case 1:
			sheet = names[0]
		default:
			return nil, errors.New("XLSX file has multiple non-empty worksheets; choose a worksheet explicitly")
		}
	} else if !contains(f.GetSheetList(), sheet) {
		return nil, fmt.Errorf("worksheet %q does not exist", sheet)
	}
	return parseXLSXSheet(f, sheet)
}

func openXLSX(r io.Reader) (*excelize.File, error) {
	f, err := excelize.OpenReader(r, excelize.Options{
		RawCellValue:      true,
		UnzipSizeLimit:    xlsxUnzipSizeLimit,
		UnzipXMLSizeLimit: xlsxUnzipXMLSizeLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("open XLSX file: %w", err)
	}
	return f, nil
}

func nonEmptySheets(f *excelize.File) ([]string, error) {
	var names []string
	for _, name := range f.GetSheetList() {
		nonEmpty, err := sheetHasVisibleContent(f, name)
		if err != nil {
			return nil, fmt.Errorf("inspect worksheet %q: %w", name, err)
		}
		if nonEmpty {
			names = append(names, name)
		}
	}
	return names, nil
}

func sheetHasVisibleContent(f *excelize.File, sheet string) (nonEmpty bool, err error) {
	rows, err := f.Rows(sheet)
	if err != nil {
		return false, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		if rows.GetRowOpts().Hidden {
			continue
		}
		cells, columnsErr := rows.Columns(excelize.Options{RawCellValue: true})
		if columnsErr != nil {
			return false, columnsErr
		}
		if len(cells) > 0 {
			return true, nil
		}
	}
	return false, rows.Error()
}

func parseXLSXSheet(f *excelize.File, sheet string) (grid *Grid, err error) {
	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, fmt.Errorf("read worksheet %q: %w", sheet, err)
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close worksheet %q: %w", sheet, closeErr)
			grid = nil
		}
	}()

	rowNumber := 0
	headerRead := false
	for rows.Next() {
		rowNumber++
		if rows.GetRowOpts().Hidden {
			continue
		}
		cells, columnsErr := rows.Columns(excelize.Options{RawCellValue: true})
		if columnsErr != nil {
			return nil, fmt.Errorf("read worksheet %q row %d: %w", sheet, rowNumber, columnsErr)
		}
		if len(cells) > MaxColumns {
			return nil, fmt.Errorf("file exceeds the limit of %d columns", MaxColumns)
		}
		if err := normalizeAndCheckCells(cells); err != nil {
			return nil, fmt.Errorf("row %d: %w", rowNumber, err)
		}
		if !headerRead {
			if len(cells) == 0 {
				return nil, errors.New("XLSX first visible row is empty; the first row must contain headers")
			}
			grid = &Grid{Headers: cells}
			headerRead = true
			continue
		}
		if len(grid.rows) == MaxDataRows {
			return nil, fmt.Errorf("file exceeds the limit of %d data rows", MaxDataRows)
		}
		row := gridRow{sourceRow: rowNumber, cells: normalizeWidth(cells, len(grid.Headers))}
		formula, cellErrors, metadataErr := xlsxRowMetadata(f, sheet, rowNumber, len(cells))
		if metadataErr != nil {
			return nil, metadataErr
		}
		if formula {
			row.warnings = append(row.warnings, "value comes from a formula; verify")
		}
		row.errors = append(row.errors, cellErrors...)
		grid.rows = append(grid.rows, row)
	}
	if rows.Error() != nil {
		return nil, fmt.Errorf("read worksheet %q: %w", sheet, rows.Error())
	}
	if !headerRead {
		return nil, errors.New("XLSX worksheet has no visible header row")
	}
	if len(grid.rows) == 0 {
		return nil, errors.New("roster file has no data rows after the header")
	}
	return grid, nil
}

func xlsxRowMetadata(f *excelize.File, sheet string, row, width int) (bool, []string, error) {
	var formula bool
	var rowErrors []string
	for column := 1; column <= width; column++ {
		cell, err := excelize.CoordinatesToCellName(column, row)
		if err != nil {
			return false, nil, fmt.Errorf("locate worksheet cell at row %d column %d: %w", row, column, err)
		}
		formulaText, err := f.GetCellFormula(sheet, cell)
		if err != nil {
			return false, nil, fmt.Errorf("inspect formula in cell %s: %w", cell, err)
		}
		formula = formula || formulaText != ""
		cellType, err := f.GetCellType(sheet, cell)
		if err != nil {
			return false, nil, fmt.Errorf("inspect type of cell %s: %w", cell, err)
		}
		if cellType == excelize.CellTypeError {
			value, valueErr := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
			if valueErr != nil {
				return false, nil, fmt.Errorf("read error cell %s: %w", cell, valueErr)
			}
			rowErrors = append(rowErrors, fmt.Sprintf("cell %s contains spreadsheet error %s", cell, value))
		}
	}
	return formula, rowErrors, nil
}

func normalizeAndCheckCells(cells []string) error {
	for i := range cells {
		if utf8.RuneCountInString(cells[i]) > MaxCellCharacters {
			return fmt.Errorf("cell %d exceeds the limit of %d characters", i+1, MaxCellCharacters)
		}
		cells[i] = strings.TrimSpace(cells[i])
	}
	return nil
}

func normalizeWidth(cells []string, width int) []string {
	normalized := make([]string, width)
	copy(normalized, cells)
	return normalized
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func utf8CSVError() error {
	return errors.New("CSV is not valid UTF-8; re-save the file as a UTF-8 CSV")
}

type utf8Reader struct {
	reader  *bufio.Reader
	pending []byte
	first   bool
}

func newUTF8Reader(r io.Reader) *utf8Reader {
	return &utf8Reader{reader: bufio.NewReader(r), first: true}
}

func (r *utf8Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	for {
		rn, size, err := r.reader.ReadRune()
		if err != nil {
			return 0, err
		}
		if rn == utf8.RuneError && size == 1 {
			return 0, errInvalidUTF8
		}
		if r.first {
			r.first = false
			if rn == '\uFEFF' {
				continue
			}
		}
		var encoded [utf8.UTFMax]byte
		n := utf8.EncodeRune(encoded[:], rn)
		copied := copy(p, encoded[:n])
		if copied < n {
			r.pending = append(r.pending, encoded[copied:n]...)
		}
		return copied, nil
	}
}
