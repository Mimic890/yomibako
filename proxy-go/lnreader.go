package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type lnReader struct {
	cfg LnReaderConfig
	sem chan struct{}
}

type lnSearchEntry struct {
	Plugin  string `json:"plugin"`
	Error   string `json:"error"`
	Results []struct {
		Path string `json:"path"`
		Name string `json:"name"`
	} `json:"results"`
}

func newLnReader(cfg LnReaderConfig) *lnReader {
	if cfg.Command == "" {
		cfg.Command = "lnreader"
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 2
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	return &lnReader{
		cfg: cfg,
		sem: make(chan struct{}, cfg.Concurrency),
	}
}

func (l *lnReader) Enabled() bool { return l.cfg.Enabled }

func (l *lnReader) Health() any {
	out, err := l.exec(5*time.Second, []string{"--version"})
	if err != nil {
		return map[string]any{
			"enabled":   l.cfg.Enabled,
			"command":   l.cfg.Command,
			"available": false,
		}
	}
	return map[string]any{
		"enabled":   l.cfg.Enabled,
		"command":   l.cfg.Command,
		"available": true,
		"version":   strings.TrimSpace(out),
	}
}

func (l *lnReader) exec(timeout time.Duration, args []string) (string, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, l.cfg.Command, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var execErr *exec.Error
		if errors.Is(err, exec.ErrNotFound) || errors.As(err, &execErr) {
			return "", fmt.Errorf("LNReader CLI not found (%q). Install it with: npm i -g @lnreader/cli", l.cfg.Command)
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" {
			detail = err.Error()
		}
		lines := strings.Split(detail, "\n")
		if len(lines) > 4 {
			lines = lines[len(lines)-4:]
		}
		return "", fmt.Errorf("lnreader %s failed: %s", args[0], strings.Join(lines, " "))
	}
	return stdout.String(), nil
}

func (l *lnReader) run(timeout time.Duration, args ...string) (string, error) {
	return l.exec(timeout, l.argsFor(args))
}

func (l *lnReader) argsFor(args []string) []string {
	out := make([]string, 0, len(args)+3)
	if l.cfg.Home != "" {
		out = append(out, "--home", l.cfg.Home)
	}
	out = append(out, args...)
	out = append(out, "--no-interactive")
	return out
}

func (l *lnReader) acquire() { l.sem <- struct{}{} }
func (l *lnReader) release() { <-l.sem }

func (l *lnReader) Search(req SearchRequest) ([]Release, error) {
	if req.Source == "" {
		return nil, fmt.Errorf("No LNReader plugin id configured")
	}
	l.acquire()
	defer l.release()

	out, err := l.run(
		120*time.Second,
		"search",
		joinNonEmpty(req.Title, req.Author),
		"--plugin", req.Source,
		"--limit", strconv.Itoa(clampLimit(req.Limit)),
		"--json",
	)
	if err != nil {
		return nil, err
	}

	raw, err := jsonSlice(out, '[', ']')
	if err != nil {
		return nil, err
	}
	var entries []lnSearchEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, err
	}

	var entry *lnSearchEntry
	for i := range entries {
		if strings.EqualFold(entries[i].Plugin, req.Source) {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("No results array for plugin %q", req.Source)
	}
	if entry.Error != "" {
		return nil, errors.New(entry.Error)
	}

	releases := make([]Release, 0, len(entry.Results))
	for _, novel := range entry.Results {
		releases = append(releases, Release{
			GUID:   novel.Path,
			Title:  novel.Name,
			Format: "epub",
		})
	}
	return releases, nil
}

func (l *lnReader) Grab(req GrabRequest, outDir string) (GrabResult, error) {
	if req.Source == "" {
		return GrabResult{}, fmt.Errorf("No LNReader plugin id configured")
	}
	l.acquire()
	defer l.release()

	out, err := l.run(0, "download", req.GUID, "--plugin", req.Source, "--out", outDir, "--json")
	if err != nil {
		return GrabResult{}, err
	}

	raw, err := jsonSlice(out, '{', '}')
	if err != nil {
		return GrabResult{}, err
	}
	var result struct {
		Files  []string `json:"files"`
		Failed []struct {
			Error string `json:"error"`
		} `json:"failed"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return GrabResult{}, err
	}

	if len(result.Files) == 0 {
		failed := make([]string, 0, 3)
		for _, item := range result.Failed {
			if item.Error != "" {
				failed = append(failed, item.Error)
			}
			if len(failed) == 3 {
				break
			}
		}
		message := "lnreader produced no EPUB"
		if len(failed) > 0 {
			message += ": " + strings.Join(failed, "; ")
		}
		return GrabResult{}, errors.New(message)
	}

	file := result.Files[0]
	if _, err := os.Stat(file); err != nil {
		return GrabResult{}, fmt.Errorf("lnreader reported %s, but it does not exist", file)
	}
	return GrabResult{FilePath: file}, nil
}

func (l *lnReader) StopIfIdle() bool { return false }
