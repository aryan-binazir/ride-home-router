package importer

import (
	"reflect"
	"testing"
)

func TestNewMappingStartsUnmapped(t *testing.T) {
	mapping := NewMapping()
	for _, binding := range mapping.columns() {
		if binding.column != UnmappedColumn {
			t.Errorf("%s column = %d, want %d", binding.field, binding.column, UnmappedColumn)
		}
	}
	if mapping.Ambiguous == nil {
		t.Fatal("Ambiguous is nil")
	}
}

func TestMappingAssignReportsMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name        string
		assignments []FieldColumn
		want        []Field
	}{
		{name: "both missing", want: []Field{FieldName, FieldAddress}},
		{name: "address missing", assignments: []FieldColumn{{Field: FieldName, Column: 0}}, want: []Field{FieldAddress}},
		{name: "none missing", assignments: []FieldColumn{{Field: FieldName, Column: 0}, {Field: FieldAddress, Column: 1}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transition := NewMapping().Assign(test.assignments, 2)
			if !reflect.DeepEqual(transition.MissingRequired, test.want) {
				t.Fatalf("MissingRequired = %#v, want %#v", transition.MissingRequired, test.want)
			}
		})
	}
}

func TestMappingAssignFirstOwnerWinsDuplicateField(t *testing.T) {
	transition := NewMapping().Assign([]FieldColumn{
		{Field: FieldName, Column: 0},
		{Field: FieldName, Column: 1},
		{Field: FieldName, Column: 2},
	}, 3)

	if transition.Mapping.NameColumn != 0 {
		t.Fatalf("NameColumn = %d, want first assigned column 0", transition.Mapping.NameColumn)
	}
	if !reflect.DeepEqual(transition.DuplicateFields, []Field{FieldName}) {
		t.Fatalf("DuplicateFields = %#v, want [%s]", transition.DuplicateFields, FieldName)
	}
	if !equalInts(transition.Mapping.Ignored, []int{1, 2}) {
		t.Fatalf("Ignored = %#v, want [1 2]", transition.Mapping.Ignored)
	}
}

func TestMappingAssignClearsOnlyResolvedAmbiguityAndRecomputesIgnored(t *testing.T) {
	base := NewMapping()
	base.Ambiguous[FieldName] = []int{0, 1}
	base.Ambiguous[FieldAddress] = []int{2, 3}
	base.Ignored = []int{4}

	transition := base.Assign([]FieldColumn{{Field: FieldName, Column: 1}}, 5)
	if transition.Mapping.NameColumn != 1 {
		t.Fatalf("NameColumn = %d, want 1", transition.Mapping.NameColumn)
	}
	if _, ok := transition.Mapping.Ambiguous[FieldName]; ok {
		t.Fatalf("resolved name ambiguity remains: %#v", transition.Mapping.Ambiguous)
	}
	if !equalInts(transition.Mapping.Ambiguous[FieldAddress], []int{2, 3}) {
		t.Fatalf("address ambiguity = %#v, want [2 3]", transition.Mapping.Ambiguous[FieldAddress])
	}
	if !equalInts(transition.Mapping.Ignored, []int{0, 4}) {
		t.Fatalf("Ignored = %#v, want [0 4]", transition.Mapping.Ignored)
	}
}

func TestMappingAssignExplicitUnmapPreservesAmbiguity(t *testing.T) {
	base := NewMapping()
	base.Ambiguous[FieldName] = []int{0, 1}

	transition := base.Assign([]FieldColumn{{Field: FieldName, Column: UnmappedColumn}}, 3)
	if !equalInts(transition.Mapping.Ambiguous[FieldName], []int{0, 1}) {
		t.Fatalf("name ambiguity = %#v, want [0 1]", transition.Mapping.Ambiguous[FieldName])
	}
	if !equalInts(transition.Mapping.Ignored, []int{2}) {
		t.Fatalf("Ignored = %#v, want [2]", transition.Mapping.Ignored)
	}
}

func TestMappingAssignPreservesFinalWidthAndOwnershipValidation(t *testing.T) {
	t.Run("out of range columns", func(t *testing.T) {
		grid := mustParseCSV(t, "name,address\nJane,1 Main St\n")
		transition := NewMapping().Assign([]FieldColumn{
			{Field: FieldName, Column: len(grid.Headers)},
			{Field: FieldAddress, Column: -2},
		}, len(grid.Headers))

		if transition.Mapping.NameColumn != 2 || transition.Mapping.AddressColumn != -2 {
			t.Fatalf("Mapping = %#v, want out-of-range assignments preserved", transition.Mapping)
		}
		if !equalInts(transition.Mapping.Ignored, []int{0, 1}) {
			t.Fatalf("Ignored = %#v, want [0 1]", transition.Mapping.Ignored)
		}
		row := Validate(grid, transition.Mapping, KindParticipant, nil)[0]
		if !hasMessage(row.Errors, "invalid column 2") || !hasMessage(row.Errors, "invalid column -2") {
			t.Fatalf("validation errors = %#v, want both invalid columns", row.Errors)
		}
	})

	t.Run("two fields own one column", func(t *testing.T) {
		grid := mustParseCSV(t, "name,address\nJane,1 Main St\n")
		transition := NewMapping().Assign([]FieldColumn{
			{Field: FieldName, Column: 0},
			{Field: FieldAddress, Column: 0},
		}, len(grid.Headers))

		if len(transition.DuplicateFields) != 0 {
			t.Fatalf("DuplicateFields = %#v, want none for a final-validator column collision", transition.DuplicateFields)
		}
		row := Validate(grid, transition.Mapping, KindParticipant, nil)[0]
		if !hasMessage(row.Errors, "cannot map to both") {
			t.Fatalf("validation errors = %#v, want column ownership error", row.Errors)
		}
	})
}

func TestMappingAssignLeavesRosterKindPolicyInValidation(t *testing.T) {
	grid := mustParseCSV(t, "name,address,capacity\nJane,1 Main St,not-a-number\n")
	transition := NewMapping().Assign([]FieldColumn{
		{Field: FieldName, Column: 0},
		{Field: FieldAddress, Column: 1},
		{Field: FieldCapacity, Column: 2},
	}, len(grid.Headers))

	participant := Validate(grid, transition.Mapping, KindParticipant, nil)[0]
	if hasMessage(participant.Errors, "capacity") {
		t.Fatalf("participant errors = %#v, capacity must remain driver-only", participant.Errors)
	}
	driver := Validate(grid, transition.Mapping, KindDriver, nil)[0]
	if !hasMessage(driver.Errors, "capacity must be a whole number") {
		t.Fatalf("driver errors = %#v, want capacity validation", driver.Errors)
	}
}

func TestMappingAssignDoesNotMutateItsInput(t *testing.T) {
	base := NewMapping()
	base.Ambiguous[FieldName] = []int{0, 1}
	base.Ignored = []int{2}
	want := copyMapping(base)

	base.Assign([]FieldColumn{{Field: FieldName, Column: 0}}, 3)
	if !reflect.DeepEqual(base, want) {
		t.Fatalf("input mapping mutated:\n got: %#v\nwant: %#v", base, want)
	}
}
