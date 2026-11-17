// Package rulate reads titles from tl.rulate.ru. The site is plain server-rendered HTML: the book
// page carries the card, /book/<id>/chapters answers with a JSON list, and a chapter is served at
// /book/<id>/<chapter>/ready.
package rulate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mimic890/yomibako/internal/imgutil"
	"github.com/Mimic890/yomibako/internal/source"
)

// Client talks to tl.rulate.ru.
type Client struct {
	Base      string
	UserAgent string
	Cookies   string
	Interval  time.Duration
	HTTP      *http.Client

	mu   sync.Mutex
	last time.Time
}

// NewClient returns a client with the public defaults.
func NewClient() *Client {
	return &Client{
		Base:      "https://tl.rulate.ru",
		UserAgent: imgutil.DefaultAgent,
		Interval:  600 * time.Millisecond,
		HTTP:      imgutil.Client(),
	}
}

// SetCookies replaces the Cookie header sent with every request.
func (c *Client) SetCookies(value string) { c.Cookies = value }

func (c *Client) wait() {
	if c.Interval <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if elapsed := time.Since(c.last); elapsed < c.Interval {
		time.Sleep(c.Interval - elapsed)
	}
	c.last = time.Now()
}

func (c *Client) get(ctx context.Context, rawURL string, accept string) ([]byte, error) {
	c.wait()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", c.UserAgent)
	request.Header.Set("Accept", accept)
	request.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")
	request.Header.Set("Referer", c.Base+"/")
	if c.Cookies != "" {
		request.Header.Set("Cookie", c.Cookies)
	}

	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("rulate: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rulate: HTTP %d на %s", response.StatusCode, rawURL)
	}
	return io.ReadAll(io.LimitReader(response.Body, 16<<20))
}

// FetchImage downloads an illustration or a cover.
func (c *Client) FetchImage(ctx context.Context, rawURL string) (source.Image, error) {
	return imgutil.Fetch(ctx, c.HTTP, rawURL, c.Base+"/")
}

// chapterEntry is one row of /book/<id>/chapters.
type chapterEntry struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

// bookID pulls the number out of "102204", "/book/102204", or "https://tl.rulate.ru/book/102204/x".
func bookID(slug string) int {
	slug = strings.TrimSpace(slug)
	if index := strings.Index(slug, "://"); index >= 0 {
		rest := slug[index+3:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			slug = rest[slash+1:]
		} else {
			return 0
		}
	}
	slug = strings.Trim(slug, "/")
	parts := strings.Split(slug, "/")
	for i, part := range parts {
		if part == "book" && i+1 < len(parts) {
			slug = parts[i+1]
			break
		}
	}
	if index := strings.IndexAny(slug, "-/"); index > 0 {
		slug = slug[:index]
	}
	id, _ := strconv.Atoi(slug)
	return id
}

var (
	ogTitlePattern = regexp.MustCompile(`(?is)<meta[^>]+property="og:title"[^>]+content="([^"]*)"`)
	ogDescPattern  = regexp.MustCompile(`(?is)<meta[^>]+property="og:description"[^>]+content="([^"]*)"`)
	ogCoverPattern = regexp.MustCompile(`(?is)<meta[^>]+property="og:image"[^>]+content="([^"]*)"`)
	authorPattern  = regexp.MustCompile(`(?is)href="/search/\?author=[^"]*"[^>]*>([^<]{2,80})</a>`)
	volumePattern  = regexp.MustCompile(`(?i)\bтом\s*(\d+)`)
	tagPattern     = regexp.MustCompile(`<[^>]+>`)
	regexpBookLink = regexp.MustCompile(`(?is)<a[^>]+href="/book/(\d+)"[^>]*>([\s\S]{0,200}?)</a>`)
)
