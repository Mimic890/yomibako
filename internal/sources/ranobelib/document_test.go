package ranobelib

import (
	"strings"
	"testing"
)

func doc(nodes ...any) map[string]any {
	return map[string]any{"type": "doc", "content": nodes}
}

func para(text string) map[string]any {
	return map[string]any{"type": "paragraph", "content": []any{
		map[string]any{"type": "text", "text": text},
	}}
}

func TestDocumentToHTMLRendersParagraphsAndMarks(t *testing.T) {
	document := doc(
		para("Первый абзац"),
		map[string]any{"type": "paragraph", "content": []any{
			map[string]any{"type": "text", "text": "жирный", "marks": []any{map[string]any{"type": "bold"}}},
		}},
		map[string]any{"type": "heading", "attrs": map[string]any{"level": float64(1)}, "content": []any{
			map[string]any{"type": "text", "text": "Заголовок"},
		}},
	)

	html := documentToHTML(document, nil, nil)
	for _, want := range []string{"<p>Первый абзац</p>", "<h2>Заголовок</h2>"} {
		if !strings.Contains(html, want) {
			t.Errorf("expected %q in %q", want, html)
		}
	}
}

func TestDocumentToHTMLEscapesText(t *testing.T) {
	html := documentToHTML(doc(para(`<script>alert("x")</script> & ещё`)), nil, nil)
	if strings.Contains(html, "<script>") {
		t.Fatalf("script tag was not escaped: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("expected escaped text, got %s", html)
	}
}

func TestDocumentToHTMLResolvesImages(t *testing.T) {
	document := doc(map[string]any{
		"type":  "image",
		"attrs": map[string]any{"images": []any{map[string]any{"image": "42"}}, "alt": "Иллюстрация"},
	})
	attachments := []map[string]any{{"id": float64(42), "url": "https://cdn.example/a.jpg", "filename": "a.jpg"}}

	html := documentToHTML(document, attachments, func(url string) string {
		if url == "https://cdn.example/a.jpg" {
			return "../images/a.jpg"
		}
		return ""
	})
	if !strings.Contains(html, `src="../images/a.jpg"`) {
		t.Fatalf("image was not resolved: %s", html)
	}
	if !strings.Contains(html, `alt="Иллюстрация"`) {
		t.Fatalf("alt text lost: %s", html)
	}
}

func TestDocumentToHTMLFallsBackToPlaceholder(t *testing.T) {
	document := doc(map[string]any{
		"type":  "image",
		"attrs": map[string]any{"images": []any{map[string]any{"image": "42"}}, "alt": "Карта"},
	})
	html := documentToHTML(document, nil, nil)
	if !strings.Contains(html, "image-placeholder") {
		t.Fatalf("expected placeholder, got %s", html)
	}
}

func TestDocumentToHTMLKeepsUnknownNodes(t *testing.T) {
	document := doc(map[string]any{
		"type":    "mysteryNode",
		"content": []any{map[string]any{"type": "text", "text": "текст внутри"}},
	})
	html := documentToHTML(document, nil, nil)
	if !strings.Contains(html, "текст внутри") {
		t.Fatalf("unknown node lost its text: %s", html)
	}
}

func TestDocumentToText(t *testing.T) {
	document := doc(para("Первый"), para("Второй"))
	text := documentToText(document)
	if !strings.Contains(text, "Первый") || !strings.Contains(text, "Второй") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestDocumentToTextFromHTMLString(t *testing.T) {
	text := documentToText("<p>Один</p><p>Два &amp; три</p>")
	if !strings.Contains(text, "Один") || !strings.Contains(text, "Два & три") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestCollectImagesDeduplicates(t *testing.T) {
	image := map[string]any{
		"type":  "image",
		"attrs": map[string]any{"images": []any{map[string]any{"image": "7"}}},
	}
	document := doc(image, image)
	attachments := []map[string]any{{"id": float64(7), "url": "https://cdn.example/7.png"}}

	refs := collectImages(document, attachments)
	if len(refs) != 1 {
		t.Fatalf("expected one image, got %d", len(refs))
	}
	if refs[0].URL != "https://cdn.example/7.png" {
		t.Fatalf("unexpected url %q", refs[0].URL)
	}
}

func TestPlainTextToHTML(t *testing.T) {
	html := plainTextToHTML("Первый\n\nВторой <b>")
	if !strings.Contains(html, "<p>Первый</p>") {
		t.Fatalf("unexpected html: %s", html)
	}
	if !strings.Contains(html, "&lt;b&gt;") {
		t.Fatalf("text was not escaped: %s", html)
	}
}
