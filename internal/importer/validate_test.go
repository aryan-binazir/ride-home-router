package importer

import (
	"fmt"
	"strings"
	"testing"
)

func TestAutoMapExactAliasesAmbiguityAndIgnoredColumns(t *testing.T) {
	headers := []string{" FULL   NAME ", "address", "Address", "location name", "latitude", "longitude", "available seats", "seats", "notes"}
	mapping := AutoMap(headers)
	if mapping.NameColumn != 0 || mapping.AddressNameColumn != 3 || mapping.CapacityColumn != 6 {
		t.Fatalf("AutoMap() = %#v", mapping)
	}
	if mapping.AddressColumn != UnmappedColumn || !equalInts(mapping.Ambiguous[FieldAddress], []int{1, 2}) {
		t.Fatalf("address mapping = %d ambiguity = %#v", mapping.AddressColumn, mapping.Ambiguous[FieldAddress])
	}
	if !equalInts(mapping.Ignored, []int{4, 5, 7, 8}) {
		t.Fatalf("ignored = %#v, want [4 5 7 8]", mapping.Ignored)
	}
}

func TestValidateRejectsMissingAndDuplicateMappings(t *testing.T) {
	grid := mustParseCSV(t, "name,address\nJane,1 Main St\n")
	mapping := AutoMap(grid.Headers)
	mapping.AddressColumn = UnmappedColumn
	rows := Validate(grid, mapping, KindParticipant, nil)
	if !hasMessage(rows[0].Errors, "required address field") {
		t.Fatalf("missing mapping errors = %#v", rows[0].Errors)
	}

	mapping = AutoMap(grid.Headers)
	mapping.AddressColumn = mapping.NameColumn
	rows = Validate(grid, mapping, KindParticipant, nil)
	if !hasMessage(rows[0].Errors, "cannot map to both") {
		t.Fatalf("duplicate mapping errors = %#v", rows[0].Errors)
	}
}

func TestValidateAddressNeedsGeocodingAndIgnoresCoordinateColumns(t *testing.T) {
	grid := mustParseCSV(t, "name,address,latitude,longitude\nJane,1 Main St,40,-73\n")
	row := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)[0]
	if len(row.Errors) != 0 || !row.NeedsGeocoding || row.HasCoordinates || row.Lat != 0 || row.Lng != 0 {
		t.Fatalf("row = %#v, want address queued for geocoding with coordinate columns ignored", row)
	}
}

func TestValidateDuplicateFlags(t *testing.T) {
	grid := mustParseCSV(t, "name,address\nJane Doe,1 Main St\n  jane   doe , 1  MAIN st \nExisting,2 Main St\nO’Brien,123 Main St.\nJ.R. O’Brien,3 Main St.\nJR OBrien,3 Main St\n")
	rows := Validate(grid, AutoMap(grid.Headers), KindParticipant, []Existing{
		{Name: " existing ", Address: "2  MAIN ST"},
		{Name: "OBrien", Address: "123 Main St"},
	})
	if rows[0].DuplicateInFile || !rows[1].DuplicateInFile {
		t.Fatalf("in-file duplicate flags = %v, %v", rows[0].DuplicateInFile, rows[1].DuplicateInFile)
	}
	if !rows[2].DuplicateOfExisting {
		t.Fatal("existing duplicate was not flagged")
	}
	if !rows[3].DuplicateOfExisting {
		t.Fatal("apostrophe and period existing duplicate was not flagged")
	}
	if rows[4].DuplicateInFile || !rows[5].DuplicateInFile {
		t.Fatalf("punctuation-normalized in-file duplicate flags = %v, %v", rows[4].DuplicateInFile, rows[5].DuplicateInFile)
	}
	if len(rows[1].Errors) != 0 || len(rows[2].Errors) != 0 {
		t.Fatalf("duplicate flags must not be errors: %#v %#v", rows[1].Errors, rows[2].Errors)
	}
}

func TestValidateDriverCapacityBoundsAndDefault(t *testing.T) {
	grid := mustParseCSV(t, "name,address,capacity\nMin,1 Main St,1\nMax,2 Main St,50\nLow,3 Main St,0\nHigh,4 Main St,51\nBlank,5 Main St,\nDecimal,6 Main St,4.5\n")
	rows := Validate(grid, AutoMap(grid.Headers), KindDriver, nil)
	if rows[0].Capacity != MinCapacity || rows[1].Capacity != MaxCapacity {
		t.Fatalf("boundary capacities = %d, %d", rows[0].Capacity, rows[1].Capacity)
	}
	for _, index := range []int{2, 3} {
		if !hasMessage(rows[index].Errors, fmt.Sprintf("between %d and %d", MinCapacity, MaxCapacity)) {
			t.Errorf("row %d errors = %#v", rows[index].SourceRow, rows[index].Errors)
		}
	}
	if !hasMessage(rows[4].Errors, "capacity is required") || !hasMessage(rows[5].Errors, "whole number") {
		t.Fatalf("blank/decimal errors = %#v / %#v", rows[4].Errors, rows[5].Errors)
	}

	grid = mustParseCSV(t, "name,address\nDefault,1 Main St\n")
	row := Validate(grid, AutoMap(grid.Headers), KindDriver, nil)[0]
	if row.Capacity != DefaultCapacity || !row.CapacityDefaulted || !hasMessage(row.Warnings, "existing drivers keep theirs") {
		t.Fatalf("default capacity row = %#v", row)
	}
}

func TestValidateAddressNameLimit(t *testing.T) {
	boundary := strings.Repeat("é", MaxAddressNameLength)
	grid := mustParseCSV(t, "name,address,address name\nJane,1 Main St,"+boundary+"\n")
	row := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)[0]
	if len(row.Errors) != 0 {
		t.Fatalf("boundary errors = %#v", row.Errors)
	}

	tooLong := strings.Repeat("x", MaxAddressNameLength+1)
	grid = mustParseCSV(t, "name,address,address name\nJane,1 Main St,"+tooLong+"\n")
	row = Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)[0]
	if !hasMessage(row.Errors, fmt.Sprintf("%d characters", MaxAddressNameLength)) {
		t.Fatalf("errors = %#v", row.Errors)
	}
}

func equalInts(a, b []int) bool {
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
