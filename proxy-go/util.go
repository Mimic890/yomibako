package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	unsafeNameRe = regexp.MustCompile(`[\x00-\x1f<>:"/\\|?*]+`)
	spaceRe      = regexp.MustCompile(`\s+`)
	imageExtRe   = regexp.MustCompile(`\.(jpe?g|png|webp|gif|avif)(?:\?|$)`)
)

func sanitizeName(title string) string {
	cleaned := unsafeNameRe.ReplaceAllString(title, " ")
	cleaned = spaceRe.ReplaceAllString(cleaned, " ")
	cleaned = strings.TrimSpace(cleaned)

	runes := []rune(cleaned)
	if len(runes) > 150 {
		runes = runes[:150]
	}
	cleaned = strings.TrimRight(string(runes), ". ")
	if cleaned == "" {
		return "book"
	}
	return cleaned
}

func jsonSlice(text string, open, close byte) (string, error) {
	start := strings.IndexByte(text, open)
	end := strings.LastIndexByte(text, close)
	if start == -1 || end <= start {
		sample := text
		if len(sample) > 200 {
			sample = sample[:200]
		}
		return "", fmt.Errorf("lnreader did not print JSON: %s", sample)
	}
	return text[start : end+1], nil
}

func clampLimit(value int) int {
	if value == 0 {
		value = 10
	}
	if value < 1 {
		value = 1
	}
	if value > 50 {
		value = 50
	}
	return value
}

func joinNonEmpty(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " ")
}

func contentDisposition(name string) string {
	var fallback strings.Builder
	for _, r := range name {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			fallback.WriteByte('_')
		} else {
			fallback.WriteRune(r)
		}
	}
	encoded := strings.ReplaceAll(url.QueryEscape(name), "+", "%20")
	return fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s", fallback.String(), encoded)
}

func extensionOf(contentType, rawURL string) string {
	contentType = strings.ToLower(contentType)
	switch {
	case strings.Contains(contentType, "png"):
		return "png"
	case strings.Contains(contentType, "webp"):
		return "webp"
	case strings.Contains(contentType, "gif"):
		return "gif"
	case strings.Contains(contentType, "avif"):
		return "avif"
	case strings.Contains(contentType, "jpeg"), strings.Contains(contentType, "jpg"):
		return "jpg"
	}

	match := imageExtRe.FindStringSubmatch(strings.ToLower(rawURL))
	if match == nil {
		return "jpg"
	}
	if match[1] == "jpeg" {
		return "jpg"
	}
	return match[1]
}
