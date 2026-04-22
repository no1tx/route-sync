package txt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCommentsInvalidAndValid(t *testing.T) {
	f, err := os.Open(fixturePath("malformed.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got := Parse(f, nil)
	if len(got) != 2 || got[0].String() != "10.0.0.0/24" || got[1].String() != "2001:db8::/32" {
		t.Fatalf("got %v", got)
	}
}

func TestParseCommentsOnly(t *testing.T) {
	f, err := os.Open(fixturePath("comments_only.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got := Parse(f, nil)
	if len(got) != 0 {
		t.Fatalf("expected no prefixes, got %v", got)
	}
}

func TestParseFixtureDuplicatesAreLeftForNormalizer(t *testing.T) {
	f, err := os.Open(fixturePath("mixed_dualstack.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got := Parse(f, nil)
	if len(got) != 5 {
		t.Fatalf("expected raw parser to keep duplicates for normalization, got %v", got)
	}
}

func fixturePath(name string) string {
	return filepath.Join("..", "..", "..", "testdata", "txt", name)
}
