// Package cbz writes comic book archives: a ZIP of page images plus a ComicInfo.xml that readers
// and BookOrbit use for metadata.
package cbz

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"

	"github.com/Mimic890/yomibako/internal/source"
)

// Meta is the metadata written into ComicInfo.xml. Fields follow the ComicInfo 2.1 element order,
// which is a sequence: readers reject a document whose elements are out of order.
type Meta struct {
	Title    string
	Series   string
	Number   int
	Volume   int
	Summary  string
	Writer   []string
	Genre    []string
	Language string
	Manga    bool
}

// Write streams a CBZ to w. Pages are named 001.jpg, 002.jpg and so on, which every reader sorts
// correctly.
func Write(w io.Writer, pages []source.Image, meta Meta) error {
	if len(pages) == 0 {
		return fmt.Errorf("cbz: no pages")
	}

	archive := zip.NewWriter(w)
	for i, page := range pages {
		ext := extensionFor(page.MediaType)
		if ext == "" {
			return fmt.Errorf("cbz: unsupported page media type %q", page.MediaType)
		}
		header := &zip.FileHeader{Name: fmt.Sprintf("%03d.%s", i+1, ext), Method: zip.Deflate}
		file, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		if _, err := file.Write(page.Bytes); err != nil {
			return err
		}
	}

	info, err := archive.Create("ComicInfo.xml")
	if err != nil {
		return err
	}
	if _, err := io.WriteString(info, comicInfo(meta, len(pages))); err != nil {
		return err
	}
	return archive.Close()
}

func comicInfo(meta Meta, pageCount int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<ComicInfo xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">` + "\n")
	writeElement(&b, "Title", meta.Title)
	writeElement(&b, "Series", meta.Series)
	if meta.Number > 0 {
		writeElement(&b, "Number", fmt.Sprint(meta.Number))
	}
	if meta.Volume > 0 {
		writeElement(&b, "Volume", fmt.Sprint(meta.Volume))
	}
	writeElement(&b, "Summary", meta.Summary)
	writeElement(&b, "Writer", strings.Join(meta.Writer, ", "))
	writeElement(&b, "Genre", strings.Join(meta.Genre, ", "))
	writeElement(&b, "PageCount", fmt.Sprint(pageCount))
	writeElement(&b, "LanguageISO", meta.Language)
	if meta.Manga {
		writeElement(&b, "Manga", "YesAndRightToLeft")
	}
	b.WriteString("</ComicInfo>\n")
	return b.String()
}

func writeElement(b *strings.Builder, name, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	fmt.Fprintf(b, "<%s>%s</%s>\n", name, escape(value), name)
}

func escape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(stripControl(value))
}

func stripControl(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r < 0x20 && r != '\n' && r != '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func extensionFor(mediaType string) string {
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
