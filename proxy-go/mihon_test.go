package main

import (
	"archive/zip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMihonGrabBuildsCBZ(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v1/sources", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"manga":[{"id":1,"name":"Test"}]}`)
	})
	mux.HandleFunc("/api/v1/manga/1/chapters", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"chapters":[{"url":"c2","chapter_number":2,"date_upload":1},{"url":"c1","chapter_number":1,"date_upload":1}]}`)
	})
	mux.HandleFunc("/api/v1/manga/1/pages", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("url") {
		case "c1":
			_, _ = io.WriteString(w, `{"pages":[{"imageUrl":"img/1.png"},{"imageUrl":"img/2.jpg"}]}`)
		case "c2":
			_, _ = io.WriteString(w, `{"pages":[{"imageUrl":"img/3.webp"}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/api/v1/proxy", func(w http.ResponseWriter, r *http.Request) {
		imageURL := r.URL.Query().Get("url")
		switch {
		case strings.HasSuffix(imageURL, ".png"):
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, "png-data")
		case strings.HasSuffix(imageURL, ".jpg"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = io.WriteString(w, "jpg-data")
		case strings.HasSuffix(imageURL, ".webp"):
			w.Header().Set("Content-Type", "image/webp")
			_, _ = io.WriteString(w, "webp-data")
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	runtime := newMihon(MihonConfig{
		Enabled:          true,
		BaseURL:          server.URL,
		StartTimeoutMS:   5000,
		ImageConcurrency: 2,
	})
	outDir := t.TempDir()
	result, err := runtime.Grab(GrabRequest{Source: "Test", GUID: "manga", Title: "Test: Book"}, outDir)
	if err != nil {
		t.Fatal(err)
	}

	wantPath := filepath.Join(outDir, "Test Book.cbz")
	if result.FilePath != wantPath {
		t.Errorf("file path = %q, want %q", result.FilePath, wantPath)
	}
	if _, err := os.Stat(result.FilePath); err != nil {
		t.Fatalf("CBZ missing: %v", err)
	}

	archive, err := zip.OpenReader(result.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()

	want := map[string]string{
		"ch-0001/0001.png":  "png-data",
		"ch-0001/0002.jpg":  "jpg-data",
		"ch-0002/0003.webp": "webp-data",
	}
	if len(archive.File) != len(want) {
		t.Fatalf("CBZ has %d entries, want %d", len(archive.File), len(want))
	}
	for _, entry := range archive.File {
		expected, ok := want[entry.Name]
		if !ok {
			t.Errorf("unexpected entry %q", entry.Name)
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != expected {
			t.Errorf("entry %q = %q, want %q", entry.Name, data, expected)
		}
	}
}

func TestMihonSourceListPicksUpNewExtensions(t *testing.T) {
	var installed atomic.Bool
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sources", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if !installed.Load() {
			_, _ = io.WriteString(w, `{"manga":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{"manga":[{"id":"42","name":"Late"}]}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	runtime := newMihon(MihonConfig{Enabled: true, BaseURL: server.URL})
	if _, err := runtime.sourceID("Late"); err == nil {
		t.Fatal("sourceID resolved a source that is not installed yet")
	}

	// The user installs the extension in miwayomi's WebUI while Yomibako is running.
	installed.Store(true)
	id, err := runtime.sourceID("late")
	if err != nil {
		t.Fatalf("sourceID after install: %v", err)
	}
	if id != "42" {
		t.Errorf("sourceID = %q, want %q", id, "42")
	}
}

func TestMihonSourceListIsCached(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sources", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"manga":[{"id":"1","name":"Cached"}]}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	runtime := newMihon(MihonConfig{Enabled: true, BaseURL: server.URL})
	for i := 0; i < 3; i++ {
		if _, err := runtime.sourceID("Cached"); err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("/api/v1/sources called %d times, want 1", got)
	}
}
