package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"  Dune: Part / Two?  ": "Dune Part Two",
		"":                      "book",
		"...":                   "book",
		"Title...":              "Title",
		"a\x00b":                "a b",
	}
	for input, want := range cases {
		if got := sanitizeName(input); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", input, got, want)
		}
	}

	long := strings.Repeat("a", 200)
	if got := len([]rune(sanitizeName(long))); got != 150 {
		t.Errorf("sanitizeName(long) has %d runes, want 150", got)
	}
}

func TestJSONSlice(t *testing.T) {
	got, err := jsonSlice("warning\n[{\"a\":1}]\n", '[', ']')
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"a":1}]`; got != want {
		t.Errorf("jsonSlice = %q, want %q", got, want)
	}
	if _, err := jsonSlice("no json here", '[', ']'); err == nil {
		t.Error("jsonSlice accepted text without JSON")
	}
}

func TestClampLimit(t *testing.T) {
	cases := map[int]int{0: 10, -5: 1, 1: 1, 50: 50, 500: 50}
	for input, want := range cases {
		if got := clampLimit(input); got != want {
			t.Errorf("clampLimit(%d) = %d, want %d", input, got, want)
		}
	}
}

func TestExtensionOf(t *testing.T) {
	cases := []struct {
		contentType string
		url         string
		want        string
	}{
		{"image/png", "", "png"},
		{"image/webp", "", "webp"},
		{"image/jpeg", "", "jpg"},
		{"", "https://example.test/page.JPEG?x=1", "jpg"},
		{"", "https://example.test/page.png?x=1", "png"},
		{"application/octet-stream", "https://example.test/page", "jpg"},
	}
	for _, tc := range cases {
		if got := extensionOf(tc.contentType, tc.url); got != tc.want {
			t.Errorf("extensionOf(%q, %q) = %q, want %q", tc.contentType, tc.url, got, tc.want)
		}
	}
}

type fakeRuntime struct {
	enabled bool
}

func (f fakeRuntime) Enabled() bool                           { return f.enabled }
func (f fakeRuntime) Health() any                             { return nil }
func (f fakeRuntime) Search(SearchRequest) ([]Release, error) { return nil, nil }
func (f fakeRuntime) StopIfIdle() bool                        { return false }
func (f fakeRuntime) Grab(_ GrabRequest, outDir string) (GrabResult, error) {
	path := filepath.Join(outDir, "book.epub")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		return GrabResult{}, err
	}
	return GrabResult{FilePath: path}, nil
}

func TestJobsRunAndSweep(t *testing.T) {
	queue, err := newJobs(1)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(queue.dir)

	job, err := queue.start("lnreader", fakeRuntime{enabled: true}, GrabRequest{Title: "Test: Book"})
	if err != nil {
		t.Fatal(err)
	}
	<-job.done
	if job.err != nil {
		t.Fatal(job.err)
	}
	if want := "Test Book.epub"; job.FileName != want {
		t.Errorf("fileName = %q, want %q", job.FileName, want)
	}
	if _, err := os.Stat(job.filePath); err != nil {
		t.Fatalf("downloaded file missing: %v", err)
	}

	job.CreatedAt = time.Now().Add(-2 * time.Minute)
	queue.sweep()
	if queue.get(job.ID) != nil {
		t.Error("expired job was not swept")
	}
	if _, err := os.Stat(filepath.Dir(job.filePath)); !os.IsNotExist(err) {
		t.Errorf("job directory still exists: %v", err)
	}
}

func TestJobsRejectDisabledRuntime(t *testing.T) {
	queue, err := newJobs(1)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(queue.dir)

	if _, err := queue.start("mihon", fakeRuntime{enabled: false}, GrabRequest{Title: "x"}); err == nil {
		t.Error("start accepted a disabled runtime")
	}
}
