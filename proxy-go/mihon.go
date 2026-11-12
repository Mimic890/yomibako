package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// sourcesTTL bounds how long the miwayomi source list is reused. Extensions can be
// installed in miwayomi's WebUI while Yomibako keeps running, so a cached list is
// refreshed on a miss and after this interval anyway.
const sourcesTTL = 5 * time.Minute

type mihon struct {
	cfg         MihonConfig
	client      *http.Client
	lastUsed    atomic.Int64
	active      atomic.Int32
	sourcesMu   sync.Mutex
	sources     []mihonSource
	sourcesTime time.Time
}

type mihonSource struct {
	ID   any    `json:"id"`
	Name string `json:"name"`
}

type mihonManga struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Author string `json:"author"`
}

type mihonChapter struct {
	URL           string  `json:"url"`
	ChapterNumber float64 `json:"chapter_number"`
	DateUpload    int64   `json:"date_upload"`
}

type mihonPage struct {
	ImageURL string `json:"imageUrl"`
	URL      string `json:"url"`
}

type imageData struct {
	data        []byte
	contentType string
}

func newMihon(cfg MihonConfig) *mihon {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.ImageConcurrency == 0 {
		cfg.ImageConcurrency = 4
	}
	if cfg.ImageConcurrency < 1 {
		cfg.ImageConcurrency = 1
	}
	if cfg.MaxChapters < 0 {
		cfg.MaxChapters = 0
	}
	if cfg.IdleMinutes == 0 {
		cfg.IdleMinutes = 10
	}
	if cfg.IdleMinutes < 1 {
		cfg.IdleMinutes = 1
	}
	if cfg.StartTimeoutMS < 1 {
		cfg.StartTimeoutMS = 30000
	}
	m := &mihon{cfg: cfg, client: &http.Client{}}
	m.lastUsed.Store(time.Now().UnixNano())
	return m
}

func (m *mihon) Enabled() bool { return m.cfg.Enabled }

func (m *mihon) Health() any {
	var container any
	var running any
	if m.cfg.Container != "" {
		container = m.cfg.Container
		running = m.containerRunning()
	}
	return map[string]any{
		"enabled":   m.cfg.Enabled,
		"container": container,
		"running":   running,
	}
}

func (m *mihon) containerRunning() bool {
	if m.cfg.Container == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", m.cfg.Container).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func (m *mihon) ensureRunning() error {
	m.lastUsed.Store(time.Now().UnixNano())
	if m.cfg.Container != "" && !m.containerRunning() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		out, err := exec.CommandContext(ctx, "docker", "start", m.cfg.Container).CombinedOutput()
		cancel()
		if err != nil {
			detail := strings.TrimSpace(string(out))
			if detail == "" {
				detail = err.Error()
			}
			return fmt.Errorf("cannot start container %q: %s", m.cfg.Container, detail)
		}
	}
	timeout := time.Duration(m.cfg.StartTimeoutMS) * time.Millisecond
	return m.waitForHealth(time.Now().Add(timeout))
}

func (m *mihon) waitForHealth(deadline time.Time) error {
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.BaseURL+"/api/v1/health", nil)
		if err != nil {
			cancel()
			return err
		}
		resp, err := m.client.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("miwayomi answered %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(time.Second)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no response")
	}
	return fmt.Errorf("miwayomi did not become healthy in time: %v", lastErr)
}

func (m *mihon) withRuntime(fn func() error) error {
	m.active.Add(1)
	m.lastUsed.Store(time.Now().UnixNano())
	defer func() {
		m.active.Add(-1)
		m.lastUsed.Store(time.Now().UnixNano())
	}()
	if err := m.ensureRunning(); err != nil {
		return err
	}
	return fn()
}

func (m *mihon) getJSON(path string, value any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("miwayomi %s answered %d", path, resp.StatusCode)
	}
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		// Usually a wrong mihon.baseUrl: another backend answering 200 with HTML.
		return fmt.Errorf("miwayomi %s did not return JSON: %w", path, err)
	}
	return nil
}

func (m *mihon) getBuffer(imageURL string) (imageData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return imageData{}, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return imageData{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return imageData{}, fmt.Errorf("Image answered %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return imageData{}, err
	}
	return imageData{data: data, contentType: resp.Header.Get("Content-Type")}, nil
}

func (m *mihon) loadSources(force bool) ([]mihonSource, error) {
	m.sourcesMu.Lock()
	defer m.sourcesMu.Unlock()

	if !force && m.sources != nil && time.Since(m.sourcesTime) < sourcesTTL {
		return m.sources, nil
	}
	var page struct {
		Manga []mihonSource `json:"manga"`
	}
	if err := m.getJSON("/api/v1/sources", &page); err != nil {
		return nil, err
	}
	m.sources = page.Manga
	m.sourcesTime = time.Now()
	return m.sources, nil
}

func findSource(sources []mihonSource, sourceName string) (string, bool) {
	wanted := strings.ToLower(strings.TrimSpace(sourceName))
	for _, source := range sources {
		if fmt.Sprint(source.ID) == wanted || strings.ToLower(source.Name) == wanted {
			return fmt.Sprint(source.ID), true
		}
	}
	return "", false
}

func (m *mihon) sourceID(sourceName string) (string, error) {
	sources, err := m.loadSources(false)
	if err != nil {
		return "", err
	}
	if id, ok := findSource(sources, sourceName); ok {
		return id, nil
	}

	// Miss: the extension was probably installed after the list was cached. Refresh once.
	if sources, err = m.loadSources(true); err != nil {
		return "", err
	}
	if id, ok := findSource(sources, sourceName); ok {
		return id, nil
	}

	known := make([]string, 0, 12)
	for _, source := range sources {
		if len(known) == 12 {
			break
		}
		known = append(known, source.Name)
	}
	loaded := strings.Join(known, ", ")
	if loaded == "" {
		loaded = "none"
	}
	return "", fmt.Errorf("no miwayomi manga source named %q. Loaded: %s", sourceName, loaded)
}

func (m *mihon) Search(req SearchRequest) ([]Release, error) {
	var releases []Release
	err := m.withRuntime(func() error {
		sourceID, err := m.sourceID(req.Source)
		if err != nil {
			return err
		}
		query := url.QueryEscape(joinNonEmpty(req.Title, req.Author))
		path := fmt.Sprintf("/api/v1/manga/%s/search?query=%s&page=1", url.PathEscape(sourceID), query)
		var page struct {
			Mangas []mihonManga `json:"mangas"`
		}
		if err := m.getJSON(path, &page); err != nil {
			return err
		}
		limit := clampLimit(req.Limit)
		if len(page.Mangas) > limit {
			page.Mangas = page.Mangas[:limit]
		}
		for _, manga := range page.Mangas {
			var author *string
			if manga.Author != "" {
				value := manga.Author
				author = &value
			}
			releases = append(releases, Release{
				GUID:   manga.URL,
				Title:  manga.Title,
				Author: author,
				Format: "cbz",
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return releases, nil
}

func (m *mihon) Grab(req GrabRequest, outDir string) (GrabResult, error) {
	outPath := filepath.Join(outDir, sanitizeName(req.Title)+".cbz")
	err := m.withRuntime(func() error {
		sourceID, err := m.sourceID(req.Source)
		if err != nil {
			return err
		}
		path := fmt.Sprintf("/api/v1/manga/%s/chapters?url=%s", url.PathEscape(sourceID), url.QueryEscape(req.GUID))
		var page struct {
			Chapters []mihonChapter `json:"chapters"`
		}
		if err := m.getJSON(path, &page); err != nil {
			return err
		}
		chapters := page.Chapters
		sort.SliceStable(chapters, func(i, j int) bool {
			if chapters[i].ChapterNumber != chapters[j].ChapterNumber {
				return chapters[i].ChapterNumber < chapters[j].ChapterNumber
			}
			return chapters[i].DateUpload < chapters[j].DateUpload
		})
		if m.cfg.MaxChapters > 0 && len(chapters) > m.cfg.MaxChapters {
			chapters = chapters[:m.cfg.MaxChapters]
		}
		if len(chapters) == 0 {
			return fmt.Errorf("%q has no chapters", req.Title)
		}
		return m.writeCBZ(sourceID, chapters, outPath)
	})
	if err != nil {
		return GrabResult{}, err
	}
	return GrabResult{FilePath: outPath}, nil
}

func (m *mihon) writeCBZ(sourceID string, chapters []mihonChapter, outPath string) (err error) {
	file, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = file.Close()
			_ = os.Remove(outPath)
		}
	}()

	zipWriter := zip.NewWriter(file)
	entry := 0
	for chapterIndex, chapter := range chapters {
		path := fmt.Sprintf("/api/v1/manga/%s/pages?url=%s", url.PathEscape(sourceID), url.QueryEscape(chapter.URL))
		var page struct {
			Pages []mihonPage `json:"pages"`
		}
		if err := m.getJSON(path, &page); err != nil {
			return err
		}

		images := make([]string, 0, len(page.Pages))
		for _, item := range page.Pages {
			imageURL := item.ImageURL
			if imageURL == "" {
				imageURL = item.URL
			}
			if imageURL != "" {
				images = append(images, imageURL)
			}
		}

		for start := 0; start < len(images); start += m.cfg.ImageConcurrency {
			end := start + m.cfg.ImageConcurrency
			if end > len(images) {
				end = len(images)
			}
			results := make([]imageData, end-start)
			errs := make([]error, end-start)
			var wg sync.WaitGroup
			for i := start; i < end; i++ {
				wg.Add(1)
				go func(slot int, imageURL string) {
					defer wg.Done()
					proxyURL := fmt.Sprintf("%s/api/v1/proxy?sourceId=%s&url=%s", m.cfg.BaseURL, url.QueryEscape(sourceID), url.QueryEscape(imageURL))
					results[slot], errs[slot] = m.getBuffer(proxyURL)
				}(i-start, images[i])
			}
			wg.Wait()

			for i := range results {
				if errs[i] != nil {
					return errs[i]
				}
				entry++
				name := fmt.Sprintf("ch-%04d/%04d.%s", chapterIndex+1, entry, extensionOf(results[i].contentType, images[start+i]))
				writer, err := zipWriter.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
				if err != nil {
					return err
				}
				if _, err := writer.Write(results[i].data); err != nil {
					return err
				}
			}
		}
	}

	if err := zipWriter.Close(); err != nil {
		return err
	}
	return file.Close()
}

func (m *mihon) StopIfIdle() bool {
	if m.cfg.Container == "" || m.active.Load() > 0 {
		return false
	}
	lastUsed := time.Unix(0, m.lastUsed.Load())
	if time.Since(lastUsed) < time.Duration(m.cfg.IdleMinutes)*time.Minute {
		return false
	}
	if !m.containerRunning() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "stop", "-t", "10", m.cfg.Container).Run(); err != nil {
		return false
	}
	return true
}
