package models

import "testing"

func TestRosterKeyMatchesEquivalentSpellings(t *testing.T) {
	tests := []struct {
		name      string
		leftName  string
		leftAddr  string
		rightName string
		rightAddr string
	}{
		{name: "ASCII apostrophe", leftName: "O'Brien", leftAddr: "1 Main St", rightName: "OBrien", rightAddr: "1 Main St"},
		{name: "left curly apostrophe", leftName: "O‘Brien", leftAddr: "1 Main St", rightName: "OBrien", rightAddr: "1 Main St"},
		{name: "curly apostrophe", leftName: "O’Brien", leftAddr: "1 Main St", rightName: "OBrien", rightAddr: "1 Main St"},
		{name: "modifier apostrophe", leftName: "OʼBrien", leftAddr: "1 Main St", rightName: "OBrien", rightAddr: "1 Main St"},
		{name: "okina apostrophe", leftName: "OʻBrien", leftAddr: "1 Main St", rightName: "OBrien", rightAddr: "1 Main St"},
		{name: "address period", leftName: "Jane Doe", leftAddr: "123 Main St.", rightName: "Jane Doe", rightAddr: "123 Main St"},
		{name: "name hyphen", leftName: "Anne-Marie", leftAddr: "1 Main St", rightName: "Anne Marie", rightAddr: "1 Main St"},
		{name: "name en dash", leftName: "Anne–Marie", leftAddr: "1 Main St", rightName: "Anne Marie", rightAddr: "1 Main St"},
		{name: "name comma", leftName: "Smith,John", leftAddr: "1 Main St", rightName: "Smith John", rightAddr: "1 Main St"},
		{name: "name zero-width space", leftName: "Anne\u200bMarie", leftAddr: "1 Main St", rightName: "AnneMarie", rightAddr: "1 Main St"},
		{name: "address comma", leftName: "Jane Doe", leftAddr: "123 Main St, Apt 2", rightName: "Jane Doe", rightAddr: "123 Main St Apt 2"},
		{name: "address en dash", leftName: "Jane Doe", leftAddr: "12–14 Main", rightName: "Jane Doe", rightAddr: "12-14 Main"},
		{name: "NFC and NFD", leftName: "Jos\u00e9", leftAddr: "1 Main St", rightName: "Jose\u0301", rightAddr: "1 Main St"},
		{name: "case and whitespace", leftName: "  John   Smith ", leftAddr: "1 Main St", rightName: "john smith", rightAddr: "1 main st"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left := RosterKey(test.leftName, test.leftAddr)
			right := RosterKey(test.rightName, test.rightAddr)
			if left != right {
				t.Fatalf("RosterKey(%q, %q) = %q, want same key as RosterKey(%q, %q) = %q", test.leftName, test.leftAddr, left, test.rightName, test.rightAddr, right)
			}
		})
	}
}

func TestRosterKeyPreservesDistinctSpellings(t *testing.T) {
	tests := []struct {
		name      string
		leftName  string
		leftAddr  string
		rightName string
		rightAddr string
	}{
		{name: "name token boundary Ann A", leftName: "Ann A", leftAddr: "1 Main St", rightName: "Anna", rightAddr: "1 Main St"},
		{name: "name token boundary Jo Ann", leftName: "Jo Ann", leftAddr: "1 Main St", rightName: "Joann", rightAddr: "1 Main St"},
		{name: "hyphenated house number", leftName: "Jane Doe", leftAddr: "12-03 Main", rightName: "Jane Doe", rightAddr: "1203 Main"},
		{name: "address hyphen versus space", leftName: "Jane Doe", leftAddr: "12-14 Main", rightName: "Jane Doe", rightAddr: "12 14 Main"},
		{name: "hyphenated unit", leftName: "Jane Doe", leftAddr: "Unit 1-2", rightName: "Jane Doe", rightAddr: "Unit 12"},
		{name: "fractional address", leftName: "Jane Doe", leftAddr: "123 1/2 Main", rightName: "Jane Doe", rightAddr: "123 12 Main"},
		{name: "address token boundary", leftName: "Jane Doe", leftAddr: "Apt 1 B", rightName: "Jane Doe", rightAddr: "Apt 1B"},
		{name: "name token order", leftName: "Smith, John", leftAddr: "1 Main St", rightName: "John Smith", rightAddr: "1 Main St"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left := RosterKey(test.leftName, test.leftAddr)
			right := RosterKey(test.rightName, test.rightAddr)
			if left == right {
				t.Fatalf("RosterKey(%q, %q) = %q, want different key from RosterKey(%q, %q)", test.leftName, test.leftAddr, left, test.rightName, test.rightAddr)
			}
		})
	}
}

func TestRosterKeyPreservesPunctuationOnlyFields(t *testing.T) {
	periodKey := RosterKey(".", "1 Main St")
	hyphenKey := RosterKey("-", "1 Main St")
	if periodKey == "" {
		t.Error("RosterKey() returned blank for a period-only name")
	}
	if hyphenKey == "" {
		t.Error("RosterKey() returned blank for a hyphen-only name")
	}
	if periodKey == hyphenKey {
		t.Errorf("RosterKey() = %q for both period-only and hyphen-only names, want different keys", periodKey)
	}
}

func TestRosterKeyExactFormat(t *testing.T) {
	got := RosterKey("Anne-Marie O'Brien", "123 Main St., Apt 2")
	want := "anne marie obrien\x00123 main st apt 2"
	if got != want {
		t.Errorf("RosterKey() = %q, want %q", got, want)
	}
}

func TestRosterKeyRequiresNameAndAddress(t *testing.T) {
	tests := []struct {
		testName string
		name     string
		address  string
	}{
		{testName: "empty name", name: "", address: "1 Main St"},
		{testName: "empty address", name: "Jane Doe", address: ""},
		{testName: "whitespace name", name: " \t\n", address: "1 Main St"},
		{testName: "whitespace address", name: "Jane Doe", address: " \t\n"},
	}

	for _, test := range tests {
		t.Run(test.testName, func(t *testing.T) {
			if got := RosterKey(test.name, test.address); got != "" {
				t.Errorf("RosterKey(%q, %q) = %q, want blank", test.name, test.address, got)
			}
		})
	}
}
