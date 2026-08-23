package importer

import (
	"fmt"
	"strings"
	"testing"
)

func TestAutoMapExactAliasesAmbiguityAndIgnoredColumns(t *testing.T) {
	headers := []string{" FULL   NAME ", "address", "Address", "location name", "latitude", "longitude", "available seats", "seats", "notes"}
	mapping := AutoMap(headers)
	if mapping.NameColumn != 0 || mapping.AddressNameColumn != 3 || mapping.LatitudeColumn != 4 || mapping.LongitudeColumn != 5 || mapping.CapacityColumn != 6 {
		t.Fatalf("AutoMap() = %#v", mapping)
	}
	if mapping.AddressColumn != UnmappedColumn || !equalInts(mapping.Ambiguous[FieldAddress], []int{1, 2}) {
		t.Fatalf("address mapping = %d ambiguity = %#v", mapping.AddressColumn, mapping.Ambiguous[FieldAddress])
	}
	if !equalInts(mapping.Ignored, []int{7, 8}) {
		t.Fatalf("ignored = %#v, want [7 8]", mapping.Ignored)
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

func TestValidateCoordinateRules(t *testing.T) {
	tests := []struct {
		name       string
		lat        string
		lng        string
		wantError  string
		wantGeo    bool
		wantCoords bool
	}{
		{name: "both empty needs geocoding", wantGeo: true},
		{name: "latitude only", lat: "40", wantError: "both be provided"},
		{name: "longitude only", lng: "-73", wantError: "both be provided"},
		{name: "latitude out of range", lat: "91", lng: "0", wantError: "between -90 and 90"},
		{name: "longitude out of range", lat: "0", lng: "-181", wantError: "between -180 and 180"},
		{name: "nan", lat: "NaN", lng: "0", wantError: "not finite"},
		{name: "infinity", lat: "0", lng: "+Inf", wantError: "not finite"},
		{name: "valid zeroes", lat: "0", lng: "0", wantCoords: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			grid := mustParseCSV(t, fmt.Sprintf("name,address,lat,lng\nJane,1 Main St,%s,%s\n", test.lat, test.lng))
			row := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)[0]
			if test.wantError != "" && !hasMessage(row.Errors, test.wantError) {
				t.Fatalf("errors = %#v, want %q", row.Errors, test.wantError)
			}
			if row.NeedsGeocoding != test.wantGeo || row.HasCoordinates != test.wantCoords {
				t.Fatalf("NeedsGeocoding=%v HasCoordinates=%v", row.NeedsGeocoding, row.HasCoordinates)
			}
		})
	}
}

func TestValidateReconcilesHouseholdCoordinatesWithinFile(t *testing.T) {
	t.Run("copies provided coordinates", func(t *testing.T) {
		grid := mustParseCSV(t, "name,address,lat,lng\nJane,1  Main St,40,-73\nJohn, 1 main st ,,\n")
		rows := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)
		if !rows[1].HasCoordinates || rows[1].NeedsGeocoding || !rows[1].CoordinatesInherited || rows[1].Lat != 40 || rows[1].Lng != -73 {
			t.Fatalf("inherited row = %#v", rows[1])
		}
		if !hasMessage(rows[1].Warnings, "another row") {
			t.Fatalf("warnings = %#v", rows[1].Warnings)
		}
	})

	t.Run("rejects conflicts", func(t *testing.T) {
		grid := mustParseCSV(t, "name,address,lat,lng\nJane,1 Main St,40,-73\nJohn,1 main st,40.00001,-73\n")
		rows := Validate(grid, AutoMap(grid.Headers), KindParticipant, nil)
		for _, row := range rows {
			if !hasMessage(row.Errors, "conflicting coordinates") {
				t.Errorf("row %d errors = %#v", row.SourceRow, row.Errors)
			}
		}
	})
}

func TestValidateExistingHouseholdCoordinatesWin(t *testing.T) {
	existing := []Existing{{Name: "Existing", Address: "1 MAIN ST", Lat: 40, Lng: -73}}
	grid := mustParseCSV(t, "name,address,lat,lng\nJane,1 Main St,41,-73\nJohn,1 main st,,\nJill,1 main st,40.0000005,-73\n")
	rows := Validate(grid, AutoMap(grid.Headers), KindParticipant, existing)
	if !hasMessage(rows[0].Errors, "conflict with the existing") {
		t.Fatalf("conflicting row errors = %#v", rows[0].Errors)
	}
	if rows[0].Lat != 41 || rows[0].Lng != -73 {
		t.Fatalf("conflicting supplied coordinates were overwritten: %#v", rows[0])
	}
	if !rows[1].CoordinatesInherited || rows[1].Lat != 40 || rows[1].Lng != -73 {
		t.Fatalf("missing row did not inherit existing coordinates: %#v", rows[1])
	}
	if len(rows[2].Errors) != 0 || rows[2].Lat != 40.0000005 || rows[2].Lng != -73 {
		t.Fatalf("tolerance-matching row = %#v", rows[2])
	}
}

func TestValidateAmbiguousExistingCoordinatesWarnAndDoNotInherit(t *testing.T) {
	existing := []Existing{
		{Name: "Existing One", Address: "1 Main St", Lat: 40, Lng: -73},
		{Name: "Existing Two", Address: "1 MAIN ST", Lat: 41, Lng: -74},
	}
	grid := mustParseCSV(t, "name,address,lat,lng\nProvided,1 Main St,42,-75\nMissing,1 main st,,\n")
	rows := Validate(grid, AutoMap(grid.Headers), KindParticipant, existing)
	for _, row := range rows {
		if len(row.Errors) != 0 || !hasMessage(row.Warnings, "existing roster entries disagree") {
			t.Fatalf("row = %#v, want warning without error", row)
		}
	}
	if rows[0].Lat != 42 || rows[0].Lng != -75 || !rows[0].HasCoordinates {
		t.Fatalf("provided row = %#v, want own coordinates", rows[0])
	}
	if !rows[1].NeedsGeocoding || rows[1].HasCoordinates || rows[1].CoordinatesInherited {
		t.Fatalf("missing row = %#v, want geocoding without inheritance", rows[1])
	}
}

func TestValidateDuplicateFlags(t *testing.T) {
	grid := mustParseCSV(t, "name,address\nJane Doe,1 Main St\n  jane   doe , 1  MAIN st \nExisting,2 Main St\n")
	rows := Validate(grid, AutoMap(grid.Headers), KindParticipant, []Existing{{Name: " existing ", Address: "2  MAIN ST", Lat: 1, Lng: 2}})
	if rows[0].DuplicateInFile || !rows[1].DuplicateInFile {
		t.Fatalf("in-file duplicate flags = %v, %v", rows[0].DuplicateInFile, rows[1].DuplicateInFile)
	}
	if !rows[2].DuplicateOfExisting {
		t.Fatal("existing duplicate was not flagged")
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
	if row.Capacity != DefaultCapacity || !hasMessage(row.Warnings, "using default capacity") {
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
