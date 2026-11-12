package main

import (
	"net/http"
	"net/http/httptest"
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

func testServer(t *testing.T, token string) *server {
	t.Helper()
	queue, err := newJobs(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(queue.dir) })
	runtimes := map[string]Runtime{
		"mihon":    fakeRuntime{enabled: true},
		"lnreader": fakeRuntime{enabled: true},
	}
	return newServer(Config{Token: token}, runtimes, queue)
}

func doRequest(s *server, method, target string, headers map[string]string) int {
	request := httptest.NewRequest(method, target, nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	s.ServeHTTP(recorder, request)
	return recorder.Code
}

func TestHealthStaysPublicWithToken(t *testing.T) {
	s := testServer(t, "secret")
	if code := doRequest(s, http.MethodGet, "/health", nil); code != http.StatusOK {
		t.Errorf("/health = %d, want %d", code, http.StatusOK)
	}
}

func TestTokenGuardsEverythingElse(t *testing.T) {
	s := testServer(t, "secret")
	if code := doRequest(s, http.MethodPost, "/v1/search", nil); code != http.StatusUnauthorized {
		t.Errorf("unauthenticated search = %d, want %d", code, http.StatusUnauthorized)
	}
	if code := doRequest(s, http.MethodPost, "/v1/search", map[string]string{"Authorization": "Bearer wrong"}); code != http.StatusUnauthorized {
		t.Errorf("wrong token = %d, want %d", code, http.StatusUnauthorized)
	}
	if code := doRequest(s, http.MethodPost, "/v1/search", map[string]string{"Authorization": "Bearer secret"}); code == http.StatusUnauthorized {
		t.Error("bearer token was rejected")
	}
	if code := doRequest(s, http.MethodPost, "/v1/search", map[string]string{"X-Proxy-Token": "secret"}); code == http.StatusUnauthorized {
		t.Error("X-Proxy-Token was rejected")
	}
}

func TestTokenAcceptedAsQueryParameter(t *testing.T) {
	// BookOrbit fetches the URL from /v1/grab itself and sends no headers, so the
	// token has to be able to travel in the URL.
	s := testServer(t, "secret")
	if code := doRequest(s, http.MethodGet, "/v1/files/unknown?token=wrong", nil); code != http.StatusUnauthorized {
		t.Errorf("wrong query token = %d, want %d", code, http.StatusUnauthorized)
	}
	if code := doRequest(s, http.MethodGet, "/v1/files/unknown?token=secret", nil); code != http.StatusNotFound {
		t.Errorf("valid query token = %d, want %d", code, http.StatusNotFound)
	}
}

func TestLnReaderArgsCarryGlobalFlags(t *testing.T) {
	runtime := newLnReader(LnReaderConfig{
		Enabled: true,
		Home:    "/home/yomibako/.lnreader",
		Cookies: "/config/cookies.txt",
	})
	got := runtime.argsFor([]string{"search", "book", "--json"})
	want := []string{
		"--home", "/home/yomibako/.lnreader",
		"--cookies", "/config/cookies.txt",
		"search", "book", "--json", "--no-interactive",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("argsFor = %v, want %v", got, want)
	}

	bare := newLnReader(LnReaderConfig{Enabled: true}).argsFor([]string{"--version"})
	if want := "--version --no-interactive"; strings.Join(bare, " ") != want {
		t.Errorf("argsFor without home/cookies = %v, want %q", bare, want)
	}
}
