package ripe

import (
	"os"
	"path/filepath"
	"testing"

	"route-sync/internal/source"
)

func TestParseRIPEResources(t *testing.T) {
	got, err := Parse(readFixture(t, "ru-normal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 || got[0].String() != "2.16.32.0/20" || got[5].String() != "2a00:1fa0::/32" {
		t.Fatalf("got %v", got)
	}
}

func TestParseEmptyRIPEResources(t *testing.T) {
	got, err := Parse(readFixture(t, "ru-empty.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty response, got %v", got)
	}
}

func TestParseMalformedRIPEFixture(t *testing.T) {
	if _, err := Parse(readFixture(t, "ru-malformed.json")); err == nil {
		t.Fatal("expected malformed JSON error")
	}
}

func TestParseDuplicateRIPEResourcesNormalize(t *testing.T) {
	got, err := Parse(readFixture(t, "ru-duplicates.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("expected raw duplicate entries, got %v", got)
	}
	normalized := source.Normalize(got, false, nil)
	if len(normalized) != 3 {
		t.Fatalf("expected duplicates to normalize to 3 prefixes, got %v", normalized)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "ripe", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
