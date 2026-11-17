package ranobelib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mimic890/yomibako/internal/logx"
)

// Defaults for the public RanobeLib API.
const (
	DefaultAPIBase  = "https://api.cdnlibs.org/api"
	DefaultSiteBase = "https://ranobelib.me"
	DefaultSiteID   = 3

	DefaultRequestInterval = 600 * time.Millisecond
	DefaultTimeout         = 25 * time.Second
	DefaultMaxImageBytes   = 30 << 20

	// Page images of manga chapters are served from this host; the API returns host-less paths.
	DefaultImageBase = "https://img3.cdnlibs.org"

	// The API answers 403 to requests that look like a bare script, so every request carries a
	// browser user agent and a referer from the site itself.
	DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// Error is a failure that carries enough context to be shown to a user.
type Error struct {
	Op     string
	Status int
	URL    string
	Err    error
}

func (e *Error) Error() string {
	switch {
	case e.Status == http.StatusTooManyRequests:
		return fmt.Sprintf("%s: RanobeLib is rate limiting requests (HTTP 429)", e.Op)
	case e.Status != 0:
		return fmt.Sprintf("%s: RanobeLib returned HTTP %d", e.Op, e.Status)
	case e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Op, e.Err)
	default:
		return e.Op
	}
}

func (e *Error) Unwrap() error { return e.Err }

// Client talks to the RanobeLib API with a fixed request interval.
type Client struct {
	APIBase       string
	SiteBase      string
	SiteID        int
	UserAgent     string
	Interval      time.Duration
	MaxImageBytes int64
	HTTP          *http.Client

	// Token is a RanobeLib access token; when set, every request carries it as a bearer token.
	Token string
	// Cookies are sent as a Cookie header, for sessions exported from a browser.
	Cookies string

	mu       sync.Mutex
	lastCall time.Time
}

// NewClient returns a client with the public defaults.
func NewClient() *Client {
	return &Client{
		APIBase:       DefaultAPIBase,
		SiteBase:      DefaultSiteBase,
		SiteID:        DefaultSiteID,
		UserAgent:     DefaultUserAgent,
		Interval:      DefaultRequestInterval,
		MaxImageBytes: DefaultMaxImageBytes,
		HTTP:          &http.Client{Timeout: DefaultTimeout},
	}
}

// wait blocks until the configured interval has passed since the previous request.
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	delay := time.Until(c.lastCall.Add(c.Interval))
	c.mu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	c.mu.Lock()
	c.lastCall = time.Now()
	c.mu.Unlock()
	return nil
}

func (c *Client) endpoint(path string, params url.Values) string {
	base := strings.TrimRight(c.APIBase, "/")
	u := base + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	return u
}

func (c *Client) do(ctx context.Context, rawURL string) (*http.Response, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, &Error{Op: "request", URL: rawURL, Err: err}
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")
	req.Header.Set("Referer", strings.TrimRight(c.SiteBase, "/")+"/")
	req.Header.Set("Origin", strings.TrimRight(c.SiteBase, "/"))
	req.Header.Set("sec-ch-ua", `"Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Linux"`)
	// The API keys some routes by site; without this header the title endpoint answers 404.
	req.Header.Set("Site-Id", strconv.Itoa(c.SiteID))
	req.Header.Set("client-time-zone", "Europe/Moscow")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.Cookies != "" {
		req.Header.Set("Cookie", c.Cookies)
	}

	logx.Debugf("GET %s", rawURL)
	// A 502 or a dropped connection from the image host says nothing about the request itself, so
	// it is retried instead of failing a volume that is already half built.
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		resp, err := c.HTTP.Do(req)
		if err == nil && resp.StatusCode < 500 {
			if resp.StatusCode != http.StatusOK {
				logx.Debugf("GET %s: HTTP %d", rawURL, resp.StatusCode)
				io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
				resp.Body.Close()
				return nil, &Error{Op: "request", URL: rawURL, Status: resp.StatusCode}
			}
			return resp, nil
		}
		if err != nil {
			lastErr = &Error{Op: "request", URL: rawURL, Err: err}
		} else {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			lastErr = &Error{Op: "request", URL: rawURL, Status: resp.StatusCode}
		}
		if attempt < 3 {
			delay := time.Duration(attempt*3) * time.Second
			logx.Warnf("GET %s: %v, повтор через %s", rawURL, lastErr, delay)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return nil, lastErr
}

// getJSON decodes a JSON response into out.
func (c *Client) getJSON(ctx context.Context, rawURL string, out any) error {
	resp, err := c.do(ctx, rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(io.LimitReader(resp.Body, 32<<20))
	if err := dec.Decode(out); err != nil {
		return &Error{Op: "decode", URL: rawURL, Err: err}
	}
	return nil
}

// Search returns one page of results.
func (c *Client) Search(ctx context.Context, query string, page int) (SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return SearchResult{}, errors.New("search query is empty")
	}
	if page < 1 {
		page = 1
	}
	params := url.Values{}
	params.Set("site_id[0]", strconv.Itoa(c.SiteID))
	params.Set("q", query)
	params.Set("page", strconv.Itoa(page))

	var payload searchResponse
	if err := c.getJSON(ctx, c.endpoint("/manga/", params), &payload); err != nil {
		return SearchResult{}, err
	}
	return normalizeSearch(payload, page, c.SiteBase)
}

// Title returns the full card for a slug.
func (c *Client) Title(ctx context.Context, slug string) (Title, error) {
	params := url.Values{}
	for _, field := range []string{"summary", "genres", "tags", "teams", "authors", "status_id", "artists"} {
		params.Add("fields[]", field)
	}
	var payload titleResponse
	if err := c.getJSON(ctx, c.endpoint("/manga/"+url.PathEscape(slug), params), &payload); err != nil {
		return Title{}, err
	}
	return normalizeTitle(payload, c.SiteBase)
}

// Chapters returns every chapter of a title.
func (c *Client) Chapters(ctx context.Context, slug string) ([]Chapter, error) {
	var payload chaptersResponse
	if err := c.getJSON(ctx, c.endpoint("/manga/"+url.PathEscape(slug)+"/chapters", nil), &payload); err != nil {
		return nil, err
	}
	return normalizeChapters(payload)
}

// ChapterContent returns the raw chapter document.
func (c *Client) ChapterContent(ctx context.Context, slug string, chapter Chapter) (chapterContent, error) {
	if strings.TrimSpace(chapter.Number) == "" {
		return chapterContent{}, errors.New("chapter has no number")
	}
	params := url.Values{}
	params.Set("number", chapter.Number)
	if chapter.Volume != "" {
		params.Set("volume", chapter.Volume)
	}
	if chapter.BranchID > 0 {
		params.Set("branch_id", strconv.Itoa(chapter.BranchID))
	}
	var payload chapterResponse
	if err := c.getJSON(ctx, c.endpoint("/manga/"+url.PathEscape(slug)+"/chapter", params), &payload); err != nil {
		return chapterContent{}, err
	}
	return payload.Data, nil
}

// PageRef is one page of a manga chapter.
type PageRef struct {
	URL    string
	Width  int
	Height int
}

// ChapterPages returns the page images of a manga chapter, in reading order.
func (c *Client) ChapterPages(ctx context.Context, slug string, chapter Chapter) ([]PageRef, error) {
	if strings.TrimSpace(chapter.Number) == "" {
		return nil, errors.New("chapter has no number")
	}
	params := url.Values{}
	params.Set("number", chapter.Number)
	if chapter.Volume != "" {
		params.Set("volume", chapter.Volume)
	}
	if chapter.BranchID > 0 {
		params.Set("branch_id", strconv.Itoa(chapter.BranchID))
	}

	var payload struct {
		Data struct {
			Pages []struct {
				URL    string `json:"url"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
			} `json:"pages"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, c.endpoint("/manga/"+url.PathEscape(slug)+"/chapter", params), &payload); err != nil {
		return nil, err
	}

	pages := make([]PageRef, 0, len(payload.Data.Pages))
	for _, page := range payload.Data.Pages {
		raw := strings.TrimSpace(page.URL)
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, "//") {
			raw = DefaultImageBase + "/" + strings.TrimPrefix(raw, "//")
		} else if !strings.HasPrefix(raw, "http") {
			raw = DefaultImageBase + "/" + strings.TrimPrefix(raw, "/")
		}
		pages = append(pages, PageRef{URL: raw, Width: page.Width, Height: page.Height})
	}
	return pages, nil
}

// Covers returns the per-volume covers of a title.
func (c *Client) Covers(ctx context.Context, slug string) ([]coverEntry, error) {
	var payload coversResponse
	if err := c.getJSON(ctx, c.endpoint("/manga/"+url.PathEscape(slug)+"/covers", nil), &payload); err != nil {
		return nil, err
	}
	return payload.Data, nil
}

// FetchImage downloads an image and detects its media type.
func (c *Client) FetchImage(ctx context.Context, rawURL string) (Image, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return Image{}, &Error{Op: "image", URL: rawURL, Err: errors.New("not an http(s) URL")}
	}
	resp, err := c.do(ctx, parsed.String())
	if err != nil {
		return Image{}, err
	}
	defer resp.Body.Close()

	if resp.ContentLength > c.MaxImageBytes {
		return Image{}, &Error{Op: "image", URL: rawURL, Err: fmt.Errorf("image is larger than %d bytes", c.MaxImageBytes)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.MaxImageBytes+1))
	if err != nil {
		return Image{}, &Error{Op: "image", URL: rawURL, Err: err}
	}
	if int64(len(body)) > c.MaxImageBytes {
		return Image{}, &Error{Op: "image", URL: rawURL, Err: fmt.Errorf("image is larger than %d bytes", c.MaxImageBytes)}
	}
	if len(body) == 0 {
		return Image{}, &Error{Op: "image", URL: rawURL, Err: errors.New("empty image")}
	}
	mediaType := detectImageMediaType(body)
	if mediaType == "" {
		return Image{}, &Error{Op: "image", URL: rawURL, Err: errors.New("response is not a supported image")}
	}
	return Image{URL: rawURL, MediaType: mediaType, Bytes: body}, nil
}
