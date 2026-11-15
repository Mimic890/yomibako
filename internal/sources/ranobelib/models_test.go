package ranobelib

import (
	"encoding/json"
	"testing"
)

func decode[T any](t *testing.T, raw string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("fixture is not valid JSON: %v", err)
	}
	return value
}

func TestNormalizeSearch(t *testing.T) {
	payload := decode[searchResponse](t, `{
	  "data": [
	    {"id": 1, "name": "English", "rus_name": "Русское", "eng_name": "English",
	     "slug": "slug", "slug_url": "1--slug",
	     "cover": {"default": "https://cdn/cover.jpg", "thumbnail": "https://cdn/thumb.jpg"},
	     "status": {"label": "Завершён"}, "type": {"label": "Китай"},
	     "rating": {"average": 4.5}, "releaseDateString": "2018 г."},
	    {"id": 2, "name": "", "rus_name": "", "eng_name": "", "slug": "no-title"}
	  ],
	  "meta": {"current_page": 2, "has_next_page": true}
	}`)

	result, err := normalizeSearch(payload, 1, "https://ranobelib.me")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected one item, got %d", len(result.Items))
	}
	item := result.Items[0]
	if item.Title != "Русское" {
		t.Errorf("title = %q", item.Title)
	}
	if item.Slug != "1--slug" {
		t.Errorf("slug = %q", item.Slug)
	}
	if item.CoverURL != "https://cdn/cover.jpg" {
		t.Errorf("cover = %q", item.CoverURL)
	}
	if item.Rating != 4.5 {
		t.Errorf("rating = %v", item.Rating)
	}
	if result.Page != 2 || !result.HasNextPage {
		t.Errorf("pagination = %+v", result)
	}
}

func TestNormalizeTitle(t *testing.T) {
	payload := decode[titleResponse](t, `{
	  "data": {
	    "id": 1, "rus_name": "Название", "eng_name": "Name", "slug": "slug", "slug_url": "1--slug",
	    "summary": {"type": "doc", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Описание"}]}]},
	    "authors": [{"name": "Автор"}], "artists": [{"name": "Художник"}],
	    "genres": [{"name": "Фэнтези"}], "tags": [{"name": "Магия"}],
	    "status": {"label": "Онгоинг"}, "type": {"label": "Китай"},
	    "rating": {"average": 4.8}, "releaseDateString": "2020 г.",
	    "cover": {"default": "https://cdn/c.jpg"}
	  }
	}`)

	title, err := normalizeTitle(payload, "https://ranobelib.me")
	if err != nil {
		t.Fatal(err)
	}
	if title.Title != "Название" || title.Description != "Описание" {
		t.Errorf("unexpected title: %+v", title)
	}
	if len(title.Authors) != 1 || title.Authors[0] != "Автор" {
		t.Errorf("authors = %v", title.Authors)
	}
	if title.SourceURL != "https://ranobelib.me/ru/book/1--slug" {
		t.Errorf("source url = %q", title.SourceURL)
	}
}

func TestNormalizeTitleRejectsEmpty(t *testing.T) {
	payload := decode[titleResponse](t, `{"data": {"slug": "x"}}`)
	if _, err := normalizeTitle(payload, "https://ranobelib.me"); err == nil {
		t.Fatal("expected an error for a title without a name")
	}
}

func TestNormalizeChaptersSortsAndMerges(t *testing.T) {
	payload := decode[chaptersResponse](t, `{
	  "data": [
	    {"index": 2, "volume": "1", "number": "2", "name": "Вторая"},
	    {"index": 1, "volume": "1", "number": "1", "name": "Первая"},
	    {"index": 1, "volume": "1", "number": "1", "name": "Первая"},
	    {"index": 3, "volume": "2", "number": "1", "name": "Том два"}
	  ]
	}`)

	chapters, err := normalizeChapters(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 3 {
		t.Fatalf("expected 3 chapters after merging, got %d", len(chapters))
	}
	if chapters[0].Number != "1" || chapters[1].Number != "2" || chapters[2].Volume != "2" {
		t.Fatalf("unexpected order: %+v", chapters)
	}
	if chapters[0].DisplayTitle != "Том 1 Глава 1 Первая" {
		t.Errorf("display title = %q", chapters[0].DisplayTitle)
	}
}

func TestNormalizeChaptersWithoutVolume(t *testing.T) {
	payload := decode[chaptersResponse](t, `{"data": [{"index": 1, "number": "1", "name": "Без тома"}]}`)
	chapters, err := normalizeChapters(payload)
	if err != nil {
		t.Fatal(err)
	}
	if chapters[0].Volume != "" {
		t.Errorf("volume = %q", chapters[0].Volume)
	}
}

func TestDetectImageMediaType(t *testing.T) {
	cases := map[string][]byte{
		"image/jpeg": {0xff, 0xd8, 0xff, 0xe0},
		"image/png":  {0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
		"image/gif":  []byte("GIF89a"),
		"image/webp": append([]byte("RIFF\x00\x00\x00\x00"), []byte("WEBP")...),
		"":           []byte("not an image"),
	}
	for want, data := range cases {
		if got := detectImageMediaType(data); got != want {
			t.Errorf("detectImageMediaType(%q) = %q, want %q", data, got, want)
		}
	}
}

func TestNormalizeChaptersCountsBranches(t *testing.T) {
	payload := decode[chaptersResponse](t, `{
	  "data": [
	    {"index": 1, "volume": "1", "number": "1", "branches": [{"id": 1}, {"id": 2}, {"id": 3}]},
	    {"index": 2, "volume": "1", "number": "2", "branches": []}
	  ]
	}`)
	chapters, err := normalizeChapters(payload)
	if err != nil {
		t.Fatal(err)
	}
	if chapters[0].Branches != 3 {
		t.Errorf("branches = %d, want 3", chapters[0].Branches)
	}
	if chapters[1].Branches != 1 {
		t.Errorf("branches = %d, want 1", chapters[1].Branches)
	}
}
