// Package library reads the BookOrbit library folder and recognises the books this service built.
//
// Every EPUB written here carries a stable identifier, "urn:ranobelib:<slug>:<volume>", so a book
// can be matched to its database row without guessing from folder names.
package library

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Entry is one recognised book in the library.
type Entry struct {
	Path       string
	Identifier string
	Slug       string
	Volume     string
}

var identifierPattern = regexp.MustCompile(`(?is)<dc:identifier[^>]*>([^<]+)</dc:identifier>`)

// Scan walks dir and returns every EPUB whose identifier belongs to this service.
func Scan(ctx context.Context, dir string) ([]Entry, error) {
	if dir == "" {
		return nil, fmt.Errorf("library: empty directory")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("library: %s is not a directory", dir)
	}

	var entries []Entry
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable folder must not stop the scan
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".epub") {
			return nil
		}
		identifier, err := readIdentifier(path)
		if err != nil {
			return nil
		}
		slug, volume, ok := parseIdentifier(identifier)
		if !ok {
			return nil
		}
		entries = append(entries, Entry{Path: path, Identifier: identifier, Slug: slug, Volume: volume})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// readIdentifier returns the dc:identifier of an EPUB.
func readIdentifier(path string) (string, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	for _, file := range reader.File {
		if !strings.HasSuffix(strings.ToLower(file.Name), ".opf") {
			continue
		}
		handle, err := file.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(handle, 1<<20))
		handle.Close()
		if err != nil {
			return "", err
		}
		match := identifierPattern.FindSubmatch(data)
		if match == nil {
			return "", fmt.Errorf("no dc:identifier in %s", path)
		}
		return strings.TrimSpace(string(match[1])), nil
	}
	return "", fmt.Errorf("no OPF in %s", path)
}

// parseIdentifier splits "urn:ranobelib:<slug>:<volume>".
func parseIdentifier(identifier string) (slug, volume string, ok bool) {
	const prefix = "urn:ranobelib:"
	if !strings.HasPrefix(identifier, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(identifier, prefix)
	index := strings.LastIndex(rest, ":")
	if index <= 0 || index == len(rest)-1 {
		return "", "", false
	}
	return rest[:index], rest[index+1:], true
}
