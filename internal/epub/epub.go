// Package epub writes EPUB 3 files without external dependencies.
//
// The archive is written by hand rather than through a generic helper because an EPUB has one
// structural rule that most writers get wrong: `mimetype` must be the first entry and must be
// stored without compression.
package epub

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"time"
)

// Metadata is everything that ends up in the OPF.
type Metadata struct {
	Title       string
	Series      string
	SeriesIndex int
	Authors     []string
	Description string
	Genres      []string
	Tags        []string
	SourceURL   string
	Identifier  string
	Language    string
	ReleaseDate string
}

// Chapter is one rendered chapter.
type Chapter struct {
	Title string
	HTML  string
}

// Image is an embedded image.
type Image struct {
	FileName  string
	MediaType string
	Bytes     []byte
}

// Book is the input of Build.
type Book struct {
	Metadata Metadata
	Chapters []Chapter
	Cover    *Image
	Images   []Image
	Modified time.Time
}

const stylesheet = `body { font-family: serif; line-height: 1.55; margin: 1.5em; }
h1 { font-size: 1.5em; line-height: 1.25; margin: 0 0 1em; }
h2, h3, h4, h5, h6 { line-height: 1.3; margin: 1.4em 0 0.6em; }
p { margin: 0 0 0.9em; text-align: justify; }
blockquote { margin: 1em 1.5em; padding-left: 1em; border-left: 3px solid #bbb; }
pre { white-space: pre-wrap; overflow-wrap: break-word; }
img { max-width: 100%; height: auto; }
.cover-image { display: block; max-width: 100%; max-height: 100vh; margin: 0 auto; }
.image-placeholder { color: #666; font-style: italic; }
.underline { text-decoration: underline; }
.authors { font-size: 1.1em; }
.description { margin-top: 1.5em; }
.source { margin-top: 2em; font-size: 0.85em; overflow-wrap: break-word; }`

// Build renders a complete EPUB 3 file.
func Build(book Book) ([]byte, error) {
	if strings.TrimSpace(book.Metadata.Title) == "" {
		return nil, fmt.Errorf("epub: title is required")
	}
	if len(book.Chapters) == 0 {
		return nil, fmt.Errorf("epub: at least one chapter is required")
	}
	modified := book.Modified
	if modified.IsZero() {
		modified = time.Now()
	}
	language := book.Metadata.Language
	if language == "" {
		language = "ru"
	}
	identifier := book.Metadata.Identifier
	if identifier == "" {
		identifier = "urn:yomibako:" + slugify(book.Metadata.Title)
	}

	type chapterDoc struct {
		id, href, path, title, html string
	}
	chapters := make([]chapterDoc, 0, len(book.Chapters))
	for i, chapter := range book.Chapters {
		name := fmt.Sprintf("chapter-%03d.xhtml", i+1)
		title := chapter.Title
		if strings.TrimSpace(title) == "" {
			title = fmt.Sprintf("Глава %d", i+1)
		}
		chapters = append(chapters, chapterDoc{
			id:    fmt.Sprintf("chapter-%d", i+1),
			href:  "text/" + name,
			path:  "OEBPS/text/" + name,
			title: title,
			html:  chapter.HTML,
		})
	}

	coverHref := ""
	if book.Cover != nil && len(book.Cover.Bytes) > 0 {
		ext := extensionFor(book.Cover.MediaType)
		if ext == "" {
			return nil, fmt.Errorf("epub: unsupported cover media type %q", book.Cover.MediaType)
		}
		coverHref = "images/cover." + ext
	}

	hasTitlePage := book.Metadata.Description != "" || len(book.Metadata.Authors) > 0

	var manifest, spine strings.Builder
	manifest.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` + "\n    ")
	manifest.WriteString(`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>` + "\n    ")
	manifest.WriteString(`<item id="css" href="style.css" media-type="text/css"/>` + "\n    ")
	if coverHref != "" {
		fmt.Fprintf(&manifest, `<item id="cover-image" href="%s" media-type="%s" properties="cover-image"/>`+"\n    ", coverHref, book.Cover.MediaType)
		fmt.Fprintf(&manifest, `<item id="cover-page" href="text/cover.xhtml" media-type="application/xhtml+xml"/>`+"\n    ")
		spine.WriteString(`<itemref idref="cover-page"/>` + "\n    ")
	}
	if hasTitlePage {
		manifest.WriteString(`<item id="title-page" href="text/title.xhtml" media-type="application/xhtml+xml"/>` + "\n    ")
		spine.WriteString(`<itemref idref="title-page"/>` + "\n    ")
	}
	for _, chapter := range chapters {
		fmt.Fprintf(&manifest, `<item id="%s" href="%s" media-type="application/xhtml+xml"/>`+"\n    ", chapter.id, chapter.href)
		fmt.Fprintf(&spine, `<itemref idref="%s"/>`+"\n    ", chapter.id)
	}
	for i, image := range book.Images {
		fmt.Fprintf(&manifest, `<item id="image-%d" href="images/%s" media-type="%s"/>`+"\n    ", i+1, image.FileName, image.MediaType)
	}

	opf := buildOPF(book.Metadata, identifier, language, modified, coverHref != "", manifest.String(), spine.String())

	navEntries := make([]navEntry, 0, len(chapters))
	for _, chapter := range chapters {
		navEntries = append(navEntries, navEntry{href: chapter.href, label: chapter.title})
	}

	entries := []entry{
		{path: "mimetype", data: []byte("application/epub+zip"), store: true},
		{path: "META-INF/container.xml", data: []byte(containerXML)},
		{path: "OEBPS/content.opf", data: []byte(opf)},
		{path: "OEBPS/style.css", data: []byte(stylesheet)},
		{path: "OEBPS/nav.xhtml", data: []byte(navXHTML(book.Metadata.Title, navEntries))},
		{path: "OEBPS/toc.ncx", data: []byte(ncx(identifier, book.Metadata.Title, navEntries))},
	}
	if coverHref != "" {
		entries = append(entries, entry{path: "OEBPS/text/cover.xhtml", data: []byte(coverXHTML(book.Metadata.Title, "../"+coverHref))})
	}
	if hasTitlePage {
		entries = append(entries, entry{path: "OEBPS/text/title.xhtml", data: []byte(titleXHTML(book.Metadata))})
	}
	for i, chapter := range chapters {
		entries = append(entries, entry{path: chapter.path, data: []byte(chapterXHTML(chapter.title, chapter.html, i))})
	}
	for _, image := range book.Images {
		entries = append(entries, entry{path: "OEBPS/images/" + image.FileName, data: image.Bytes})
	}
	if coverHref != "" {
		entries = append(entries, entry{path: "OEBPS/" + coverHref, data: book.Cover.Bytes})
	}

	return writeZip(entries, modified)
}

type navEntry struct{ href, label string }

func buildOPF(meta Metadata, identifier, language string, modified time.Time, hasCover bool, manifest, spine string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&b, `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id" xml:lang="%s">`+"\n", escape(language))
	b.WriteString(`  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">` + "\n")
	fmt.Fprintf(&b, "    <dc:identifier id=\"book-id\">%s</dc:identifier>\n", escape(identifier))
	fmt.Fprintf(&b, "    <dc:title>%s</dc:title>\n", escape(meta.Title))
	fmt.Fprintf(&b, "    <dc:language>%s</dc:language>\n", escape(language))
	for _, author := range meta.Authors {
		fmt.Fprintf(&b, "    <dc:creator>%s</dc:creator>\n", escape(author))
	}
	if meta.Description != "" {
		fmt.Fprintf(&b, "    <dc:description>%s</dc:description>\n", escape(meta.Description))
	}
	if year := releaseYear(meta.ReleaseDate); year != "" {
		fmt.Fprintf(&b, "    <dc:date>%s</dc:date>\n", year)
	}
	if meta.SourceURL != "" {
		fmt.Fprintf(&b, "    <dc:source>%s</dc:source>\n", escape(meta.SourceURL))
	}
	for _, subject := range unique(append(append([]string{}, meta.Genres...), meta.Tags...)) {
		fmt.Fprintf(&b, "    <dc:subject>%s</dc:subject>\n", escape(subject))
	}
	if meta.Series != "" {
		fmt.Fprintf(&b, "    <meta name=\"calibre:series\" content=\"%s\"/>\n", escape(meta.Series))
		if meta.SeriesIndex > 0 {
			fmt.Fprintf(&b, "    <meta name=\"calibre:series_index\" content=\"%d\"/>\n", meta.SeriesIndex)
		}
		fmt.Fprintf(&b, "    <meta property=\"belongs-to-collection\" id=\"series-id\">%s</meta>\n", escape(meta.Series))
		b.WriteString("    <meta refines=\"#series-id\" property=\"collection-type\">series</meta>\n")
		if meta.SeriesIndex > 0 {
			fmt.Fprintf(&b, "    <meta refines=\"#series-id\" property=\"group-position\">%d</meta>\n", meta.SeriesIndex)
		}
	}
	fmt.Fprintf(&b, "    <meta property=\"dcterms:modified\">%s</meta>\n", modified.UTC().Format("2006-01-02T15:04:05Z"))
	if hasCover {
		b.WriteString("    <meta name=\"cover\" content=\"cover-image\"/>\n")
	}
	b.WriteString("  </metadata>\n  <manifest>\n    ")
	b.WriteString(manifest)
	b.WriteString("\n  </manifest>\n  <spine toc=\"ncx\">\n    ")
	b.WriteString(spine)
	b.WriteString("\n  </spine>\n</package>\n")
	return b.String()
}

const containerXML = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`

func xhtmlHead(title, prefix string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="ru" lang="ru">
<head>
<meta charset="UTF-8"/>
<title>` + escape(title) + `</title>
<link rel="stylesheet" type="text/css" href="` + prefix + `style.css"/>
</head>`
}

func chapterXHTML(title, body string, index int) string {
	return xhtmlHead(title, "../") + `
<body>
<section epub:type="chapter" id="chapter-` + fmt.Sprint(index+1) + `">
<h1>` + escape(title) + `</h1>
` + body + `
</section>
</body>
</html>
`
}

func coverXHTML(title, href string) string {
	return xhtmlHead("Обложка", "../") + `
<body>
<section epub:type="cover" id="cover">
<img class="cover-image" src="` + escape(href) + `" alt="Обложка: ` + escape(title) + `"/>
</section>
</body>
</html>
`
}

func titleXHTML(meta Metadata) string {
	var b strings.Builder
	b.WriteString(xhtmlHead(meta.Title, "../"))
	b.WriteString("\n<body>\n<section epub:type=\"titlepage\" id=\"titlepage\">\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", escape(meta.Title))
	if len(meta.Authors) > 0 {
		fmt.Fprintf(&b, `<p class="authors">%s</p>`+"\n", escape(strings.Join(meta.Authors, ", ")))
	}
	if meta.Description != "" {
		b.WriteString(`<div class="description">`)
		for _, paragraph := range strings.Split(meta.Description, "\n\n") {
			if strings.TrimSpace(paragraph) == "" {
				continue
			}
			fmt.Fprintf(&b, "<p>%s</p>", strings.ReplaceAll(escape(paragraph), "\n", "<br/>"))
		}
		b.WriteString("</div>\n")
	}
	if meta.SourceURL != "" {
		fmt.Fprintf(&b, `<p class="source">Источник: <a href="%s">%s</a></p>`+"\n", escape(meta.SourceURL), escape(meta.SourceURL))
	}
	b.WriteString("</section>\n</body>\n</html>\n")
	return b.String()
}

func navXHTML(title string, entries []navEntry) string {
	var b strings.Builder
	b.WriteString(xhtmlHead(title, ""))
	b.WriteString("\n<body>\n<nav epub:type=\"toc\" id=\"toc\">\n<h1>Содержание</h1>\n<ol>\n")
	for _, entry := range entries {
		fmt.Fprintf(&b, `<li><a href="%s">%s</a></li>`+"\n", escape(entry.href), escape(entry.label))
	}
	b.WriteString("</ol>\n</nav>\n</body>\n</html>\n")
	return b.String()
}

func ncx(identifier, title string, entries []navEntry) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">` + "\n<head>\n")
	fmt.Fprintf(&b, `<meta name="dtb:uid" content="%s"/>`+"\n", escape(identifier))
	b.WriteString(`<meta name="dtb:depth" content="1"/>` + "\n")
	b.WriteString(`<meta name="dtb:totalPageCount" content="0"/>` + "\n")
	b.WriteString(`<meta name="dtb:maxPageNumber" content="0"/>` + "\n")
	b.WriteString("</head>\n")
	fmt.Fprintf(&b, "<docTitle><text>%s</text></docTitle>\n<navMap>\n", escape(title))
	for i, entry := range entries {
		fmt.Fprintf(&b, "<navPoint id=\"navPoint-%d\" playOrder=\"%d\">\n<navLabel><text>%s</text></navLabel>\n<content src=\"%s\"/>\n</navPoint>\n",
			i+1, i+1, escape(entry.label), escape(entry.href))
	}
	b.WriteString("</navMap>\n</ncx>\n")
	return b.String()
}

// ── ZIP ──────────────────────────────────────────────────────────────────────────────────────────

type entry struct {
	path  string
	data  []byte
	store bool
}

func writeZip(entries []entry, modified time.Time) ([]byte, error) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	seen := make(map[string]struct{}, len(entries))

	for _, item := range entries {
		if _, ok := seen[item.path]; ok {
			return nil, fmt.Errorf("epub: duplicate entry %q", item.path)
		}
		seen[item.path] = struct{}{}

		header := &zip.FileHeader{Name: item.path, Method: zip.Deflate, Modified: modified}
		if item.store {
			header.Method = zip.Store
		}
		file, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := file.Write(item.data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────────────────────────

func escape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
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

// releaseYear keeps only the four-digit year: the source returns strings like "2018 г." and
// dc:date must be a date, not a sentence.
func releaseYear(value string) string {
	for i := 0; i+4 <= len(value); i++ {
		candidate := value[i : i+4]
		if candidate[0] < '1' || candidate[0] > '2' {
			continue
		}
		digits := true
		for _, r := range candidate {
			if r < '0' || r > '9' {
				digits = false
				break
			}
		}
		if digits {
			return candidate
		}
	}
	return ""
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
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

func slugify(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
