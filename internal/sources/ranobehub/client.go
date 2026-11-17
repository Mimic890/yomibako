// Package ranobehub reads titles from ranobehub.org. The site is a Next.js app: the pages carry
// the card, and a small JSON API serves chapter lists and chapter bodies.
package ranobehub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Mimic890/yomibako/internal/imgutil"
	"github.com/Mimic890/yomibako/internal/source"
)

// Client talks to ranobehub.org.
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
		Base:      "https://ranobehub.org",
		UserAgent: imgutil.DefaultAgent,
		Interval:  400 * time.Millisecond,
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

// get fetches a URL and returns the body.
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
		return nil, fmt.Errorf("ranobehub: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ranobehub: HTTP %d на %s", response.StatusCode, rawURL)
	}
	return io.ReadAll(io.LimitReader(response.Body, 8<<20))
}

func (c *Client) getJSON(ctx context.Context, rawURL string, target any) error {
	body, err := c.get(ctx, rawURL, "application/json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("ranobehub: не разобрал ответ %s: %w", rawURL, err)
	}
	return nil
}

// FetchImage downloads an illustration.
func (c *Client) FetchImage(ctx context.Context, rawURL string) (source.Image, error) {
	return imgutil.Fetch(ctx, c.HTTP, rawURL, c.Base)
}

// ── API shapes ────────────────────────────────────────────────────────────────────────────────

type chapterItem struct {
	ID     int    `json:"id"`
	Volume int    `json:"volume"`
	Number int    `json:"number"`
	Title  string `json:"title"`
}

type chaptersResponse struct {
	Items []chapterItem `json:"items"`
	Total int           `json:"total"`
}

type chapterResponse struct {
	Chapter struct {
		ID    int    `json:"id"`
		HTML  string `json:"html"`
		Title string `json:"title"`
	} `json:"chapter"`
}

type searchItem struct {
	ID        int    `json:"id"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	PosterURL string `json:"posterUrl"`
}

type searchResponse struct {
	Books []searchItem `json:"books"`
}

// normalizeSlug turns "1034-bleach-cant-fear-your-own-world" into a page path and an id.
func normalizeSlug(slug string) (string, int) {
	slug = strings.Trim(strings.TrimSpace(slug), "/")
	if index := strings.Index(slug, "://"); index >= 0 {
		slug = slug[strings.Index(slug, "://")+3:]
		if slash := strings.Index(slug, "/"); slash >= 0 {
			slug = slug[slash+1:]
		}
	}
	parts := strings.Split(slug, "/")
	slug = parts[len(parts)-1]

	id := 0
	if index := strings.Index(slug, "-"); index > 0 {
		fmt.Sscanf(slug[:index], "%d", &id)
	} else {
		fmt.Sscanf(slug, "%d", &id)
	}
	return slug, id
}
