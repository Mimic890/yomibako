package ranobelib

import "strings"

// CoverMap turns the covers response into volume label -> full-resolution URL. The volume label
// sits in `info`, the `volume` field is empty for every entry seen so far; both are checked.
func CoverMap(entries []coverEntry) map[string]string {
	covers := make(map[string]string, len(entries))
	for _, entry := range entries {
		label := firstText(entry.Info, entry.Volume)
		if label == "" {
			continue
		}
		url := firstText(entry.Cover["orig"], entry.Cover["default"], entry.Cover["md"], entry.Cover["thumbnail"])
		if url == "" {
			continue
		}
		if _, exists := covers[label]; !exists {
			covers[label] = url
		}
	}
	return covers
}

// NormalizeSlug puts the id separator back: a link copied from the address bar can come as
// "9113-mato-seihei-no-slave" while the API keys titles by "9113--mato-seihei-no-slave".
func NormalizeSlug(slug string) string {
	slug = strings.Trim(strings.TrimSpace(slug), "/")
	index := 0
	for index < len(slug) && slug[index] >= '0' && slug[index] <= '9' {
		index++
	}
	if index == 0 || index >= len(slug) {
		return slug
	}
	if strings.HasPrefix(slug[index:], "--") {
		return slug
	}
	if slug[index] != '-' {
		return slug
	}
	return slug[:index] + "-" + slug[index:]
}
