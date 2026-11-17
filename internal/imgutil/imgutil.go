// Package imgutil downloads images for the sources that scrape plain HTML.
package imgutil

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mimic890/yomibako/internal/source"
)

// MaxBytes caps one image. Volume covers and illustrations are well under this.
const MaxBytes = 32 << 20

// Fetch downloads an image and works out its media type from the first bytes, because the sites
// often answer with a generic content type.
func Fetch(ctx context.Context, client *http.Client, rawURL string, referer string) (source.Image, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return source.Image{}, fmt.Errorf("image %s: not an http(s) URL", rawURL)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return source.Image{}, err
	}
	request.Header.Set("User-Agent", DefaultAgent)
	request.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,*/*;q=0.8")
	if referer != "" {
		request.Header.Set("Referer", referer)
	}

	response, err := client.Do(request)
	if err != nil {
		return source.Image{}, fmt.Errorf("image %s: %w", rawURL, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return source.Image{}, fmt.Errorf("image %s: HTTP %d", rawURL, response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, MaxBytes+1))
	if err != nil {
		return source.Image{}, fmt.Errorf("image %s: %w", rawURL, err)
	}
	if len(body) == 0 {
		return source.Image{}, fmt.Errorf("image %s: empty", rawURL)
	}
	if len(body) > MaxBytes {
		return source.Image{}, fmt.Errorf("image %s: larger than %d bytes", rawURL, MaxBytes)
	}

	mediaType := Detect(body)
	if mediaType == "" {
		return source.Image{}, errors.New("image " + rawURL + ": not a supported image")
	}
	return source.Image{URL: rawURL, MediaType: mediaType, Bytes: body}, nil
}

const DefaultAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// Detect reads the magic bytes.
func Detect(body []byte) string {
	prefix := body
	if len(prefix) > 512 {
		prefix = prefix[:512]
	}
	switch {
	case len(prefix) > 3 && prefix[0] == 0xff && prefix[1] == 0xd8 && prefix[2] == 0xff:
		return "image/jpeg"
	case len(prefix) > 8 && string(prefix[1:4]) == "PNG":
		return "image/png"
	case len(prefix) > 6 && (string(prefix[:6]) == "GIF87a" || string(prefix[:6]) == "GIF89a"):
		return "image/gif"
	case len(prefix) > 12 && string(prefix[:4]) == "RIFF" && string(prefix[8:12]) == "WEBP":
		return "image/webp"
	case len(prefix) > 4 && string(prefix[:4]) == "%PDF":
		return ""
	}
	return ""
}

// Extension is the file suffix for a media type.
func Extension(mediaType string) string {
	switch strings.ToLower(mediaType) {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	}
	return ""
}

// Hash is a short stable name for a URL, used as an image file name.
func Hash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:6])
}

// Client is an http client tuned for scraping. IPv4 is tried first: several of these sites fail a
// TLS handshake over IPv6 and only answer on the A record.
func Client() *http.Client {
	dialer := &net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, "tcp4", address)
			if err == nil {
				return conn, nil
			}
			return dialer.DialContext(ctx, network, address)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   20 * time.Second,
		ExpectContinueTimeout: 2 * time.Second,
	}
	return &http.Client{Timeout: 120 * time.Second, Transport: transport}
}
