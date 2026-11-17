// Package comx reads comics, manga and manhwa from com-x.life. The site runs on DLE with a
// JavaScript gate in front of it: every page is either the real document or a challenge that has to
// be solved in a browser, so the service relies on the cookies of a logged-in browser instead of
// pretending to be one.
package comx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Mimic890/yomibako/internal/imgutil"
	"github.com/Mimic890/yomibako/internal/source"
)

// ErrGuard is returned when the site answers with its JavaScript challenge instead of the page.
var ErrGuard = fmt.Errorf("com-x: сайт просит пройти проверку браузера - нужны свежие куки с com-x.life в cookies.txt")

// Client talks to com-x.life.
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
		Base:      "https://com-x.life",
		UserAgent: imgutil.DefaultAgent,
		Interval:  800 * time.Millisecond,
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

// get fetches a page and refuses the challenge page, which a browser would solve and a plain
// client cannot.
func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	c.wait()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", c.UserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")
	request.Header.Set("Referer", c.Base+"/")
	if c.Cookies != "" {
		request.Header.Set("Cookie", c.Cookies)
	}

	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("com-x: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("com-x: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
			return nil, ErrGuard
		}
		return nil, fmt.Errorf("com-x: HTTP %d на %s", response.StatusCode, rawURL)
	}
	if isChallenge(body) {
		return nil, ErrGuard
	}
	return body, nil
}

// isChallenge recognises the gate: it is a small page with a proof-of-work script and no content.
func isChallenge(body []byte) bool {
	if len(body) > 20000 {
		return false
	}
	text := string(body)
	return strings.Contains(text, "__guard_trust") ||
		strings.Contains(text, "Для доступа к сайту необходимо включить JavaScript") ||
		strings.Contains(text, "pow_nonce")
}

// FetchImage downloads a page image.
func (c *Client) FetchImage(ctx context.Context, rawURL string) (source.Image, error) {
	return imgutil.Fetch(ctx, c.HTTP, rawURL, c.Base+"/")
}
