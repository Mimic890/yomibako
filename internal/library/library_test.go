package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mimic890/yomibako/internal/epub"
)

func TestParseIdentifier(t *testing.T) {
	cases := []struct {
		identifier string
		slug       string
		volume     string
		ok         bool
	}{
		{"urn:ranobelib:20818--lord-of-the-mysteries:3", "20818--lord-of-the-mysteries", "3", true},
		{"urn:ranobelib:1--x:0", "1--x", "0", true},
		{"urn:other:1--x:3", "", "", false},
		{"urn:ranobelib:1--x", "", "", false},
	}
	for _, tc := range cases {
		slug, volume, ok := parseIdentifier(tc.identifier)
		if ok != tc.ok || slug != tc.slug || volume != tc.volume {
			t.Errorf("parseIdentifier(%q) = %q, %q, %v", tc.identifier, slug, volume, ok)
		}
	}
}

func writeBook(t *testing.T, dir, name, identifier string) {
	t.Helper()
	data, err := epub.Build(epub.Book{
		Metadata: epub.Metadata{Title: "Книга", Identifier: identifier},
		Chapters: []epub.Chapter{{Title: "Глава 1", HTML: "<p>текст</p>"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanFindsOurBooks(t *testing.T) {
	root := t.TempDir()
	writeBook(t, filepath.Join(root, "Автор", "Книга", "01"), "book.epub", "urn:ranobelib:1--x:1")
	writeBook(t, filepath.Join(root, "Автор", "Книга", "02"), "book.epub", "urn:ranobelib:1--x:2")
	writeBook(t, filepath.Join(root, "Другое"), "other.epub", "urn:other:book:1")

	entries, err := Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Slug != "1--x" || entries[0].Volume != "1" {
		t.Errorf("unexpected entry: %+v", entries[0])
	}
}

func TestScanRejectsMissingDirectory(t *testing.T) {
	if _, err := Scan(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected an error for a missing directory")
	}
}
