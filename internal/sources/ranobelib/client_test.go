package ranobelib

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(server *httptest.Server) *Client {
	client := NewClient()
	client.APIBase = server.URL
	client.SiteBase = "https://ranobelib.me"
	client.Interval = 0
	client.HTTP = server.Client()
	return client
}

func TestClientSendsSiteHeaders(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": [], "meta": {"current_page": 1}}`))
	}))
	defer server.Close()

	client := testClient(server)
	if _, err := client.Search(context.Background(), "тест", 1); err != nil {
		t.Fatal(err)
	}

	for header, want := range map[string]string{
		"Site-Id":         "3",
		"Referer":         "https://ranobelib.me/",
		"Origin":          "https://ranobelib.me",
		"Accept":          "application/json, text/plain, */*",
		"User-Agent":      DefaultUserAgent,
		"Accept-Language": "ru-RU,ru;q=0.9,en;q=0.8",
	} {
		if got.Get(header) != want {
			t.Errorf("%s = %q, want %q", header, got.Get(header), want)
		}
	}
}

func TestClientSearchBuildsQuery(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Write([]byte(`{"data": [], "meta": {"current_page": 1}}`))
	}))
	defer server.Close()

	client := testClient(server)
	if _, err := client.Search(context.Background(), "  повелитель тайн  ", 2); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"q=%D0%BF%D0%BE%D0%B2%D0%B5%D0%BB%D0%B8%D1%82%D0%B5%D0%BB%D1%8C", "page=2", "site_id%5B0%5D=3"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q does not contain %q", query, want)
		}
	}
}

func TestClientReportsHTTPErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.Search(context.Background(), "тест", 1)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error does not mention the status: %v", err)
	}
}

func TestClientRejectsNonImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>not an image</html>"))
	}))
	defer server.Close()

	client := testClient(server)
	if _, err := client.FetchImage(context.Background(), server.URL+"/x.jpg"); err == nil {
		t.Fatal("expected an error for a non-image response")
	}
}

func TestClientAcceptsImage(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(png)
	}))
	defer server.Close()

	client := testClient(server)
	image, err := client.FetchImage(context.Background(), server.URL+"/x.png")
	if err != nil {
		t.Fatal(err)
	}
	if image.MediaType != "image/png" {
		t.Errorf("media type = %q", image.MediaType)
	}
}

func TestClientRespectsInterval(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": [], "meta": {"current_page": 1}}`))
	}))
	defer server.Close()

	client := testClient(server)
	client.Interval = 120 * time.Millisecond

	started := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := client.Search(context.Background(), "тест", 1); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(started); elapsed < 240*time.Millisecond {
		t.Errorf("three requests took %s, expected at least 240ms", elapsed)
	}
}
