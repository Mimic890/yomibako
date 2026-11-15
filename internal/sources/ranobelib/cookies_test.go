package ranobelib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	content := strings.Join([]string{
		"# Netscape HTTP Cookie File",
		"",
		".ranobelib.me\tTRUE\t/\tTRUE\t0\ttoken\tsecret",
		".cdnlibs.org\tTRUE\t/\tTRUE\t0\tsession\tabc",
		".example.com\tTRUE\t/\tFALSE\t0\tother\tnope",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	header, err := LoadCookies(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(header, "token=secret") || !strings.Contains(header, "session=abc") {
		t.Errorf("missing cookies: %q", header)
	}
	if strings.Contains(header, "other=") {
		t.Errorf("foreign cookie leaked: %q", header)
	}
}

func TestLoadCookiesRejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte("# nothing here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCookies(path); err == nil {
		t.Fatal("expected an error for a file without RanobeLib cookies")
	}
}
