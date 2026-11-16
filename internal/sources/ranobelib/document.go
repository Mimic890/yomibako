package ranobelib

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// RanobeLib returns chapter bodies as a ProseMirror-like document:
//
//	{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"..."}]}]}
//
// Older records return a plain HTML string instead. Both shapes are rendered here, and unknown
// node types fall back to their children so a malformed document never produces a broken EPUB.

var controlChars = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f]`)

// escapeHTML escapes text for XHTML and drops control characters that XML forbids.
func escapeHTML(value string) string {
	return html.EscapeString(controlChars.ReplaceAllString(value, ""))
}

var (
	brTag     = regexp.MustCompile(`(?i)<br\s*/?>`)
	blockEnd  = regexp.MustCompile(`(?i)</(p|div|li|h[1-6]|blockquote|tr)>`)
	anyTag    = regexp.MustCompile(`<[^>]*>`)
	entityRef = regexp.MustCompile(`&(#x?[0-9a-fA-F]+|[a-zA-Z]+);`)
)

// htmlToText is a best-effort plain text conversion, used for summaries returned as HTML.
func htmlToText(value string) string {
	s := brTag.ReplaceAllString(value, "\n")
	s = blockEnd.ReplaceAllString(s, "\n\n")
	s = anyTag.ReplaceAllString(s, "")
	s = decodeEntities(s)
	s = regexp.MustCompile(`[ \t]*\n[ \t]*`).ReplaceAllString(s, "\n")
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func decodeEntities(value string) string {
	return entityRef.ReplaceAllStringFunc(value, func(match string) string {
		entity := match[1 : len(match)-1]
		switch {
		case strings.HasPrefix(entity, "#x"), strings.HasPrefix(entity, "#X"):
			if code, err := strconv.ParseInt(entity[2:], 16, 32); err == nil && code >= 0 && code <= 0x10ffff {
				return string(rune(code))
			}
		case strings.HasPrefix(entity, "#"):
			if code, err := strconv.ParseInt(entity[1:], 10, 32); err == nil && code >= 0 && code <= 0x10ffff {
				return string(rune(code))
			}
		}
		switch strings.ToLower(entity) {
		case "nbsp":
			return " "
		case "amp":
			return "&"
		case "lt":
			return "<"
		case "gt":
			return ">"
		case "quot":
			return `"`
		case "apos":
			return "'"
		}
		return match
	})
}

// documentToText flattens a document or HTML string into plain text.
func documentToText(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return htmlToText(v)
	case map[string]any:
		if text(v["type"]) == "doc" {
			return strings.TrimSpace(childrenToText(v["content"]))
		}
		return strings.TrimSpace(nodeToText(v))
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func childrenToText(value any) string {
	items := array(value)
	var b strings.Builder
	for _, item := range items {
		b.WriteString(nodeToText(item))
	}
	return b.String()
}

func nodeToText(node any) string {
	m := object(node)
	if m == nil {
		return ""
	}
	content := m["content"]
	switch text(m["type"]) {
	case "text":
		return text(m["text"])
	case "hardBreak", "horizontalRule":
		return "\n"
	case "paragraph", "heading", "blockquote", "codeBlock":
		return strings.TrimSpace(childrenToText(content)) + "\n\n"
	case "listItem":
		return "- " + strings.TrimSpace(childrenToText(content)) + "\n"
	case "orderedList", "bulletList":
		return childrenToText(content) + "\n"
	case "image":
		if alt := text(object(m["attrs"])["alt"]); alt != "" {
			return "[" + alt + "]"
		}
		return ""
	default:
		return childrenToText(content)
	}
}

// imageRef is one image reference found in a document.
type imageRef struct {
	URL      string
	Alt      string
	FileName string
}

// imageReferences returns the attachment references of an image node.
func imageReferences(node map[string]any) []any {
	attrs := object(node["attrs"])
	if images := array(attrs["images"]); len(images) > 0 {
		out := make([]any, 0, len(images))
		for _, entry := range images {
			if m := object(entry); m != nil {
				out = append(out, m["image"])
			} else {
				out = append(out, entry)
			}
		}
		return out
	}
	if src := attrs["src"]; src != nil {
		return []any{src}
	}
	return nil
}

func findAttachment(attachments []map[string]any, reference any) map[string]any {
	ref := fmt.Sprint(reference)
	for _, attachment := range attachments {
		if fmt.Sprint(attachment["id"]) == ref || fmt.Sprint(attachment["name"]) == ref || fmt.Sprint(attachment["filename"]) == ref {
			return attachment
		}
	}
	return nil
}

func resolveImageURL(attachments []map[string]any, reference any) (string, string) {
	if attachment := findAttachment(attachments, reference); attachment != nil {
		return text(attachment["url"]), text(attachment["filename"])
	}
	if ref := text(reference); strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref, ""
	}
	return "", ""
}

// collectImages walks a document and returns every image it uses, in order, without duplicates.
func collectImages(document any, attachments []map[string]any) []imageRef {
	seen := make(map[string]struct{})
	var out []imageRef

	if raw, ok := document.(string); ok {
		for _, url := range htmlImageSources(raw) {
			if _, exists := seen[url]; exists {
				continue
			}
			seen[url] = struct{}{}
			out = append(out, imageRef{URL: url})
		}
		return out
	}

	var walk func(node any)
	walk = func(node any) {
		m := object(node)
		if m == nil {
			return
		}
		if text(m["type"]) == "image" {
			alt := text(object(m["attrs"])["alt"])
			for _, reference := range imageReferences(m) {
				url, fileName := resolveImageURL(attachments, reference)
				if url == "" {
					continue
				}
				if _, ok := seen[url]; ok {
					continue
				}
				seen[url] = struct{}{}
				out = append(out, imageRef{URL: url, Alt: alt, FileName: fileName})
			}
		}
		for _, child := range array(m["content"]) {
			walk(child)
		}
	}
	walk(document)
	return out
}

// documentToHTML renders a document as an XHTML fragment. imageHref maps a remote URL to a local
// href inside the EPUB; returning "" renders an accessible placeholder instead.
func documentToHTML(document any, attachments []map[string]any, imageHref func(url string) string) string {
	switch v := document.(type) {
	case nil:
		return ""
	case string:
		return htmlStringToXHTML(v, imageHref)
	case map[string]any:
		if text(v["type"]) == "doc" {
			return strings.TrimSpace(renderChildren(v["content"], attachments, imageHref))
		}
		return strings.TrimSpace(renderNode(v, attachments, imageHref))
	default:
		return plainTextToHTML(fmt.Sprint(v))
	}
}

var (
	imgTag       = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	imgSrc       = regexp.MustCompile(`(?is)\bsrc\s*=\s*["']([^"']+)["']`)
	dangerBlocks = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`),
		regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`),
		regexp.MustCompile(`(?is)<iframe\b[^>]*>.*?</iframe>`),
		regexp.MustCompile(`(?is)<object\b[^>]*>.*?</object>`),
		regexp.MustCompile(`(?is)<embed\b[^>]*>.*?</embed>`),
	}
	eventAttr = regexp.MustCompile(`(?is)\son[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	jsHref    = regexp.MustCompile(`(?is)href\s*=\s*["']\s*javascript:[^"']*["']`)
)

// htmlImageSources lists the image URLs of an HTML fragment.
func htmlImageSources(raw string) []string {
	var urls []string
	for _, tag := range imgTag.FindAllString(raw, -1) {
		if match := imgSrc.FindStringSubmatch(tag); match != nil {
			urls = append(urls, match[1])
		}
	}
	return urls
}

// htmlStringToXHTML renders a chapter that the API returned as an HTML string. Images are
// rewritten to their local copies, and anything executable is dropped: the fragment ends up inside
// an EPUB, where a script tag is both useless and invalid.
func htmlStringToXHTML(raw string, imageHref func(string) string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	cleaned := raw
	for _, block := range dangerBlocks {
		cleaned = block.ReplaceAllString(cleaned, "")
	}
	cleaned = eventAttr.ReplaceAllString(cleaned, "")
	cleaned = jsHref.ReplaceAllString(cleaned, "")

	cleaned = imgTag.ReplaceAllStringFunc(cleaned, func(tag string) string {
		match := imgSrc.FindStringSubmatch(tag)
		if match == nil {
			return ""
		}
		href := ""
		if imageHref != nil {
			href = imageHref(match[1])
		}
		if href == "" {
			return ""
		}
		return `<img src="` + escapeHTML(href) + `" alt=""/>`
	})

	if strings.TrimSpace(anyTag.ReplaceAllString(cleaned, "")) == "" && !strings.Contains(cleaned, "<img") {
		return ""
	}
	return cleaned
}

// plainTextToHTML wraps plain text into paragraphs.
func plainTextToHTML(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	paragraphs := regexp.MustCompile(`\n{2,}`).Split(value, -1)
	out := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if strings.TrimSpace(paragraph) == "" {
			continue
		}
		out = append(out, "<p>"+strings.ReplaceAll(escapeHTML(paragraph), "\n", "<br/>")+"</p>")
	}
	return strings.Join(out, "\n")
}

func renderChildren(value any, attachments []map[string]any, imageHref func(string) string) string {
	var b strings.Builder
	for _, child := range array(value) {
		b.WriteString(renderNode(child, attachments, imageHref))
	}
	return b.String()
}

func renderNode(node any, attachments []map[string]any, imageHref func(string) string) string {
	m := object(node)
	if m == nil {
		return ""
	}
	inner := func() string { return renderChildren(m["content"], attachments, imageHref) }

	switch text(m["type"]) {
	case "doc":
		return inner()
	case "text":
		return strings.ReplaceAll(escapeHTML(text(m["text"])), "\n", "<br/>")
	case "hardBreak":
		return "<br/>"
	case "horizontalRule":
		return "<hr/>"
	case "paragraph":
		if body := inner(); body != "" {
			return "<p>" + body + "</p>"
		}
		return "<p><br/></p>"
	case "heading":
		level := headingLevel(m)
		return fmt.Sprintf("<h%d>%s</h%d>", level, inner(), level)
	case "blockquote":
		return "<blockquote>" + inner() + "</blockquote>"
	case "orderedList":
		return "<ol>" + inner() + "</ol>"
	case "bulletList":
		return "<ul>" + inner() + "</ul>"
	case "listItem":
		return "<li>" + inner() + "</li>"
	case "bold":
		return "<strong>" + inner() + "</strong>"
	case "italic":
		return "<em>" + inner() + "</em>"
	case "underline":
		return `<span class="underline">` + inner() + "</span>"
	case "strike":
		return "<s>" + inner() + "</s>"
	case "code":
		return "<code>" + inner() + "</code>"
	case "codeBlock":
		return "<pre><code>" + inner() + "</code></pre>"
	case "link":
		href := safeHref(text(object(m["attrs"])["href"]))
		if href == "" {
			return inner()
		}
		return `<a href="` + escapeHTML(href) + `">` + inner() + `</a>`
	case "image":
		return renderImage(m, attachments, imageHref)
	default:
		return inner()
	}
}

func headingLevel(node map[string]any) int {
	level := int(numberOrZero(object(node["attrs"])["level"])) + 1
	if level < 2 {
		level = 2
	}
	if level > 6 {
		level = 6
	}
	return level
}

func safeHref(value string) string {
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:") {
		return value
	}
	return ""
}

func renderImage(node map[string]any, attachments []map[string]any, imageHref func(string) string) string {
	alt := text(object(node["attrs"])["alt"])
	for _, reference := range imageReferences(node) {
		url, _ := resolveImageURL(attachments, reference)
		if url == "" {
			continue
		}
		href := ""
		if imageHref != nil {
			href = imageHref(url)
		}
		if href != "" {
			return `<img src="` + escapeHTML(href) + `" alt="` + escapeHTML(alt) + `"/>`
		}
	}
	if alt != "" {
		return `<p class="image-placeholder">[` + escapeHTML(alt) + `]</p>`
	}
	return ""
}
