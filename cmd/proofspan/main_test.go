package main

import (
	"strings"
	"testing"
)

// parsePins is the eval command's --assertions parser. Fail-closed rule:
// a non-empty --assertions value that produces zero pins is a typo'd or
// malformed pin list — running the DEFAULT pin instead would green-light
// an eval the user thinks ran their assertion. It must error, naming the
// offending part.

func TestParsePinsEmptyMeansDefault(t *testing.T) {
	pins, err := parsePins("")
	if err != nil {
		t.Fatalf("empty string must mean default pins, got %v", err)
	}
	if len(pins) != 0 {
		t.Fatalf("empty string yields no explicit pins (caller applies default), got %v", pins)
	}
}

func TestParsePinsParsesIDAndVersion(t *testing.T) {
	pins, err := parsePins("span-correlation@1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 1 || pins[0].id != "span-correlation" || pins[0].ver != "1.0.0" {
		t.Fatalf("pins = %+v", pins)
	}
}

func TestParsePinsParsesCSVWithSpaces(t *testing.T) {
	pins, err := parsePins("a@1.0.0, b@2.0.0 ,")
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 2 {
		t.Fatalf("want 2 pins, got %+v", pins)
	}
	if pins[0].id != "a" || pins[1].id != "b" || pins[1].ver != "2.0.0" {
		t.Fatalf("pins = %+v", pins)
	}
}

func TestParsePinsRejectsPartWithoutVersion(t *testing.T) {
	_, err := parsePins("span-correlation@1.0.0,garbage")
	if err == nil {
		t.Fatal("a pin without @version must error, not silently fall back to defaults")
	}
	if !strings.Contains(err.Error(), "garbage") {
		t.Errorf("error must name the offending pin: %v", err)
	}
}

func TestParsePinsRejectsEmptyVersion(t *testing.T) {
	_, err := parsePins("a@,b@2.0.0")
	if err == nil {
		t.Fatal("empty version must error")
	}
	if !strings.Contains(err.Error(), "a@") {
		t.Errorf("error must name the offending pin: %v", err)
	}
}
