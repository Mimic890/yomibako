package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const maxBodyBytes = 64 << 10

type Config struct {
	Host           string         `json:"host"`
	Port           int            `json:"port"`
	Token          string         `json:"token"`
	FileTTLMinutes int            `json:"fileTtlMinutes"`
	Mihon          MihonConfig    `json:"mihon"`
	LnReader       LnReaderConfig `json:"lnreader"`
}

type MihonConfig struct {
	Enabled          bool   `json:"enabled"`
	Container        string `json:"container"`
	BaseURL          string `json:"baseUrl"`
	StartTimeoutMS   int    `json:"startTimeoutMs"`
	IdleMinutes      int    `json:"idleMinutes"`
	ImageConcurrency int    `json:"imageConcurrency"`
	MaxChapters      int    `json:"maxChapters"`
}

type LnReaderConfig struct {
	Enabled     bool   `json:"enabled"`
	Command     string `json:"command"`
	Home        string `json:"home"`
	Concurrency int    `json:"concurrency"`
}

func defaultConfig() Config {
	return Config{
		Host:           "127.0.0.1",
		Port:           7345,
		FileTTLMinutes: 60,
		Mihon: MihonConfig{
			Enabled:          true,
			Container:        "miwayomi",
			BaseURL:          "http://127.0.0.1:4567",
			StartTimeoutMS:   30000,
			IdleMinutes:      10,
			ImageConcurrency: 4,
		},
		LnReader: LnReaderConfig{
			Enabled:     true,
			Command:     "lnreader",
			Concurrency: 2,
		},
	}
}

func loadConfig() (Config, error) {
	cfg := defaultConfig()
	path := os.Getenv("YOMIBAKO_CONFIG")
	if path == "" {
		path = "config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return cfg, fmt.Errorf("cannot read %s: %w", path, err)
		}
	} else if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("cannot read %s: %w", path, err)
	}

	if v := os.Getenv("YOMIBAKO_HOST"); v != "" {
		cfg.Host = v
	}
	if v := os.Getenv("YOMIBAKO_PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid proxy port: %s", v)
		}
		cfg.Port = port
	}
	if v := os.Getenv("YOMIBAKO_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("MIWAYOMI_URL"); v != "" {
		cfg.Mihon.BaseURL = v
	}

	cfg.Host = strings.TrimSpace(cfg.Host)
	if cfg.Port < 1 || cfg.Port > 65535 {
		return cfg, fmt.Errorf("invalid proxy port: %d", cfg.Port)
	}
	return cfg, nil
}

type Runtime interface {
	Enabled() bool
	Health() any
	Search(SearchRequest) ([]Release, error)
	Grab(GrabRequest, string) (GrabResult, error)
	StopIfIdle() bool
}

type SearchRequest struct {
	Source string
	Title  string
	Author string
	Limit  int
}

type GrabRequest struct {
	Source string
	GUID   string
	Title  string
}

type Release struct {
	GUID    string  `json:"guid"`
	Title   string  `json:"title"`
	Author  *string `json:"author"`
	Format  string  `json:"format"`
	InfoURL *string `json:"infoUrl"`
}

type GrabResult struct {
	FilePath string
}

type server struct {
	cfg      Config
	runtimes map[string]Runtime
	jobs     *jobs
	mux      *http.ServeMux
}

func newServer(cfg Config, runtimes map[string]Runtime, jobs *jobs) *server {
	s := &server{cfg: cfg, runtimes: runtimes, jobs: jobs}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /v1/search", s.handleSearch)
	mux.HandleFunc("POST /v1/grab", s.handleGrab)
	mux.HandleFunc("GET /v1/files/{id}", s.handleFile)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
	})
	s.mux = mux
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Token != "" && !authorized(r, s.cfg.Token) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	health := make(map[string]any, len(s.runtimes))
	for name, runtime := range s.runtimes {
		health[name] = runtime.Health()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "runtimes": health})
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Runtime string `json:"runtime"`
		Source  string `json:"source"`
		Title   string `json:"title"`
		Author  string `json:"author"`
		Limit   int    `json:"limit"`
	}
	if err := readJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	runtime, err := s.pickRuntime(body.Runtime)
	if err != nil {
		s.fail(w, err)
		return
	}
	source, err := requireString(body.Source, "source")
	if err != nil {
		s.fail(w, err)
		return
	}
	title, err := requireString(body.Title, "title")
	if err != nil {
		s.fail(w, err)
		return
	}

	releases, err := runtime.Search(SearchRequest{
		Source: source,
		Title:  title,
		Author: body.Author,
		Limit:  body.Limit,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": releases})
}

func (s *server) handleGrab(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Runtime string `json:"runtime"`
		Source  string `json:"source"`
		GUID    string `json:"guid"`
		Title   string `json:"title"`
	}
	if err := readJSON(w, r, &body); err != nil {
		s.fail(w, err)
		return
	}
	runtime, err := s.pickRuntime(body.Runtime)
	if err != nil {
		s.fail(w, err)
		return
	}
	source, err := requireString(body.Source, "source")
	if err != nil {
		s.fail(w, err)
		return
	}
	guid, err := requireString(body.GUID, "guid")
	if err != nil {
		s.fail(w, err)
		return
	}
	title, err := requireString(body.Title, "title")
	if err != nil {
		s.fail(w, err)
		return
	}

	job, err := s.jobs.start(body.Runtime, runtime, GrabRequest{Source: source, GUID: guid, Title: title})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"id":       job.ID,
		"fileName": job.FileName,
		"format":   job.Format,
		"url":      "/v1/files/" + job.ID,
	})
}

func (s *server) handleFile(w http.ResponseWriter, r *http.Request) {
	job := s.jobs.get(r.PathValue("id"))
	if job == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Unknown download"})
		return
	}
	select {
	case <-job.done:
	case <-r.Context().Done():
		return
	}
	if job.err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": job.err.Error()})
		return
	}
	if job.filePath == "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Download has no file"})
		return
	}
	info, err := os.Stat(job.filePath)
	if err != nil || !info.Mode().IsRegular() {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Downloaded file disappeared"})
		return
	}
	file, err := os.Open(job.filePath)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Downloaded file disappeared"})
		return
	}
	defer file.Close()

	contentType := "application/epub+zip"
	if job.Format == "cbz" {
		contentType = "application/vnd.comicbook+zip"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", contentDisposition(job.FileName))
	http.ServeContent(w, r, job.FileName, info.ModTime(), file)
}

func (s *server) pickRuntime(name string) (Runtime, error) {
	runtime, ok := s.runtimes[name]
	if !ok {
		return nil, httpError{http.StatusBadRequest, fmt.Sprintf("Unknown runtime %q", name)}
	}
	if !runtime.Enabled() {
		return nil, httpError{http.StatusBadRequest, fmt.Sprintf("Runtime %q is disabled", name)}
	}
	return runtime, nil
}

func (s *server) fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var he httpError
	if errors.As(err, &he) {
		status = he.status
	}
	if status >= 500 {
		log.Printf("error: %v", err)
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

type httpError struct {
	status  int
	message string
}

func (e httpError) Error() string { return e.message }

func authorized(r *http.Request, token string) bool {
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ") == token
	}
	return r.Header.Get("X-Proxy-Token") == token
}

func requireString(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", httpError{http.StatusBadRequest, fmt.Sprintf("%q is required", field)}
	}
	return value, nil
}

func readJSON(w http.ResponseWriter, r *http.Request, value any) error {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return httpError{http.StatusRequestEntityTooLarge, "Request body is too large"}
		}
		return httpError{http.StatusBadRequest, err.Error()}
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, value); err != nil {
		return httpError{http.StatusBadRequest, "Request body is not JSON"}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, `{"error":"Internal error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	runtimes := map[string]Runtime{
		"mihon":    newMihon(cfg.Mihon),
		"lnreader": newLnReader(cfg.LnReader),
	}
	jobs, err := newJobs(cfg.FileTTLMinutes)
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(jobs.dir)

	httpServer := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Handler:           newServer(cfg, runtimes, jobs),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			for name, runtime := range runtimes {
				if runtime.StopIfIdle() {
					log.Printf("%s runtime stopped after idle", name)
				}
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			jobs.sweep()
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	log.Printf("Yomibako on http://%s", httpServer.Addr)
	if cfg.Mihon.Enabled {
		container := cfg.Mihon.Container
		if container == "" {
			container = "external"
		}
		idle := cfg.Mihon.IdleMinutes
		if idle < 1 {
			idle = 1
		}
		log.Printf("  mihon:    %s (container %s, idle %dm)", cfg.Mihon.BaseURL, container, idle)
	} else {
		log.Printf("  mihon:    disabled")
	}
	if cfg.LnReader.Enabled {
		command := cfg.LnReader.Command
		if command == "" {
			command = "lnreader"
		}
		home := ""
		if cfg.LnReader.Home != "" {
			home = fmt.Sprintf(" (home %s)", cfg.LnReader.Home)
		}
		log.Printf("  lnreader: %s%s", command, home)
	} else {
		log.Printf("  lnreader: disabled")
	}

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
