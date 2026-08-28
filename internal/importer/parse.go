package importer

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

const (
	xlsxUnzipSizeLimit    = 64 << 20
	xlsxUnzipXMLSizeLimit = 8 << 20
	// MaxFormulaMetadataXMLBytes limits inflated XLSX metadata.
	MaxFormulaMetadataXMLBytes int64 = 32 << 20

	formulaMetadataWarning = "Formula information could not be read from this workbook; values calculated by formulas may not be flagged."
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

// Sheets lists XLSX sheets with visible rows.
func Sheets(r io.Reader) (names []string, err error) {
	if r == nil {
		return nil, errors.New("roster file is empty")
	}
	f, _, err := openXLSX(r)
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
	if cellsContainControlCharacters(header) {
		return nil, errors.New("header row: cell contains control characters")
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
			return nil, fmt.Errorf("read CSV row %d: %w", csvRecordLine(reader, record, readErr), readErr)
		}
		sourceRow := csvRecordLine(reader, record, nil)
		if len(grid.rows) == MaxDataRows {
			return nil, fmt.Errorf("file exceeds the limit of %d data rows", MaxDataRows)
		}
		if len(record) > MaxColumns {
			return nil, fmt.Errorf("file exceeds the limit of %d columns", MaxColumns)
		}
		containsControlCharacters := cellsContainControlCharacters(record)
		if err := normalizeAndCheckCells(record); err != nil {
			return nil, fmt.Errorf("row %d: %w", sourceRow, err)
		}

		row := gridRow{sourceRow: sourceRow}
		if containsControlCharacters {
			row.errors = append(row.errors, "cell contains control characters")
		}
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

func csvRecordLine(reader *csv.Reader, record []string, readErr error) int {
	if len(record) > 0 {
		line, _ := reader.FieldPos(0)
		return line
	}
	if parseErr, ok := errors.AsType[*csv.ParseError](readErr); ok {
		return parseErr.StartLine
	}
	return 1
}

func parseXLSX(r io.Reader, requestedSheet string) (grid *Grid, err error) {
	f, data, err := openXLSX(r)
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
	return parseXLSXSheet(f, data, sheet)
}

func openXLSX(r io.Reader) (*excelize.File, []byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, fmt.Errorf("read XLSX file: %w", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{
		RawCellValue:      true,
		UnzipSizeLimit:    xlsxUnzipSizeLimit,
		UnzipXMLSizeLimit: xlsxUnzipXMLSizeLimit,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("open XLSX file: %w", err)
	}
	return f, data, nil
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
		if cellsHaveContent(cells) {
			return true, nil
		}
	}
	return false, rows.Error()
}

func parseXLSXSheet(f *excelize.File, data []byte, sheet string) (grid *Grid, err error) {
	formulaRows, err := xlsxFormulaRows(data, sheet)
	formulaMetadataUnavailable := err != nil
	if err != nil {
		formulaRows = map[int]struct{}{}
	}
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
		containsControlCharacters := cellsContainControlCharacters(cells)
		if err := normalizeAndCheckCells(cells); err != nil {
			return nil, fmt.Errorf("row %d: %w", rowNumber, err)
		}
		if !headerRead {
			if containsControlCharacters {
				return nil, errors.New("header row: cell contains control characters")
			}
			if !cellsHaveContent(cells) {
				return nil, errors.New("XLSX first visible row is empty; the first row must contain headers")
			}
			grid = &Grid{Headers: cells}
			if formulaMetadataUnavailable {
				grid.Warnings = append(grid.Warnings, formulaMetadataWarning)
			} else if _, formula := formulaRows[rowNumber]; formula {
				grid.Warnings = append(grid.Warnings, formulaMetadataWarning)
			}
			headerRead = true
			continue
		}
		if !cellsHaveContent(cells) {
			continue
		}
		if len(grid.rows) == MaxDataRows {
			return nil, fmt.Errorf("file exceeds the limit of %d data rows", MaxDataRows)
		}
		row := gridRow{sourceRow: rowNumber, cells: normalizeWidth(cells, len(grid.Headers)), xlsx: true}
		if containsControlCharacters {
			row.errors = append(row.errors, "cell contains control characters")
		}
		if _, formula := formulaRows[rowNumber]; formula {
			row.warnings = append(row.warnings, "value comes from a formula; verify")
		}
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

var xlsxErrorValues = map[string]bool{
	"#NULL!": true, "#DIV/0!": true, "#VALUE!": true, "#REF!": true,
	"#NAME?": true, "#NUM!": true, "#N/A": true, "#GETTING_DATA": true,
	"#SPILL!": true, "#CALC!": true, "#FIELD!": true, "#BLOCKED!": true,
	"#UNKNOWN!": true, "#CONNECT!": true, "#BUSY!": true,
}

type xlsxWorkbookDocument struct {
	Sheets []struct {
		Name           string `xml:"name,attr"`
		RelationshipID string `xml:"id,attr"`
	} `xml:"sheets>sheet"`
}

type xlsxRelationshipsDocument struct {
	Relationships []struct {
		ID     string `xml:"Id,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

func xlsxFormulaRows(data []byte, sheet string) (map[int]struct{}, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	entries := make(map[string]*zip.File, len(archive.File))
	for _, entry := range archive.File {
		entries[strings.ToLower(strings.ReplaceAll(entry.Name, "\\", "/"))] = entry
	}

	var workbook xlsxWorkbookDocument
	if err := decodeXLSXEntry(entries["xl/workbook.xml"], &workbook); err != nil {
		return nil, err
	}
	var relationships xlsxRelationshipsDocument
	if err := decodeXLSXEntry(entries["xl/_rels/workbook.xml.rels"], &relationships); err != nil {
		return nil, err
	}

	relationshipID := ""
	for _, candidate := range workbook.Sheets {
		if strings.EqualFold(candidate.Name, sheet) {
			relationshipID = candidate.RelationshipID
			break
		}
	}
	if relationshipID == "" {
		return nil, fmt.Errorf("worksheet %q is missing from workbook metadata", sheet)
	}
	target := ""
	for _, relationship := range relationships.Relationships {
		if relationship.ID == relationshipID {
			target = strings.TrimPrefix(strings.ReplaceAll(relationship.Target, "\\", "/"), "/")
			break
		}
	}
	if !strings.HasPrefix(strings.ToLower(target), "xl/") {
		target = path.Join("xl", target)
	}
	entry := entries[strings.ToLower(path.Clean(target))]
	if entry == nil {
		return nil, fmt.Errorf("worksheet %q XML is missing", sheet)
	}

	contents, err := readXLSXMetadataEntry(entry)
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(contents))
	formulaRows := make(map[int]struct{})
	currentRow := 0
	for {
		token, tokenErr := decoder.Token()
		if errors.Is(tokenErr, io.EOF) {
			break
		}
		if tokenErr != nil {
			return nil, tokenErr
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "row":
			currentRow++
			for _, attr := range start.Attr {
				if attr.Name.Local == "r" {
					if parsed, parseErr := strconv.Atoi(attr.Value); parseErr == nil {
						currentRow = parsed
					}
				}
			}
		case "c":
			var cell struct {
				Formula *struct{} `xml:"f"`
			}
			if err := decoder.DecodeElement(&cell, &start); err != nil {
				return nil, err
			}
			if cell.Formula != nil {
				formulaRows[currentRow] = struct{}{}
			}
		}
	}
	return formulaRows, nil
}

func decodeXLSXEntry(entry *zip.File, target any) error {
	contents, err := readXLSXMetadataEntry(entry)
	if err != nil {
		return err
	}
	return xml.NewDecoder(bytes.NewReader(contents)).Decode(target)
}

func readXLSXMetadataEntry(entry *zip.File) ([]byte, error) {
	if entry == nil {
		return nil, errors.New("required XLSX metadata is missing")
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	contents, err := io.ReadAll(io.LimitReader(reader, MaxFormulaMetadataXMLBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > MaxFormulaMetadataXMLBytes {
		return nil, fmt.Errorf("XLSX formula metadata exceeds the inflated limit of %d bytes", MaxFormulaMetadataXMLBytes)
	}
	return contents, nil
}

func cellsHaveContent(cells []string) bool {
	for _, cell := range cells {
		if strings.TrimSpace(cell) != "" {
			return true
		}
	}
	return false
}

func normalizeAndCheckCells(cells []string) error {
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
		if utf8.RuneCountInString(cells[i]) > MaxCellCharacters {
			return fmt.Errorf("cell %d exceeds the limit of %d characters", i+1, MaxCellCharacters)
		}
	}
	return nil
}

func cellsContainControlCharacters(cells []string) bool {
	for _, cell := range cells {
		for _, character := range cell {
			if character < ' ' && character != '\t' {
				return true
			}
		}
	}
	return false
}

func normalizeWidth(cells []string, width int) []string {
	normalized := make([]string, width)
	copy(normalized, cells)
	return normalized
}

func contains(values []string, target string) bool {
	return slices.Contains(values, target)
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
