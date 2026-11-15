package ranobelib

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Mimic890/yomibako/internal/source"
)

// The public models live in the source package; these aliases keep the implementation readable.
type (
	SearchResult   = source.SearchResult
	SearchItem     = source.SearchItem
	Title          = source.Title
	Chapter        = source.Chapter
	Image          = source.Image
	ChapterContent = source.ChapterContent
)

// ── Raw API shapes ───────────────────────────────────────────────────────────────────────────────
//
// The API is loose about types: numbers arrive as strings, empty fields arrive as "" instead of
// null, and nested objects are sometimes missing. Everything is decoded into `any` and converted
// through the helpers below rather than trusting a struct tag.

type searchResponse struct {
	Data []map[string]any `json:"data"`
	Meta struct {
		CurrentPage any  `json:"current_page"`
		PerPage     any  `json:"per_page"`
		HasNextPage bool `json:"has_next_page"`
	} `json:"meta"`
	Links struct {
		Next any `json:"next"`
	} `json:"links"`
}

type titleResponse struct {
	Data map[string]any `json:"data"`
}

type chaptersResponse struct {
	Data []map[string]any `json:"data"`
}

type chapterResponse struct {
	Data chapterContent `json:"data"`
}

type coversResponse struct {
	Data []coverEntry `json:"data"`
}

type coverEntry struct {
	ID     any            `json:"id"`
	Info   any            `json:"info"`
	Order  any            `json:"order"`
	Volume any            `json:"volume"`
	Cover  map[string]any `json:"cover"`
}

type chapterContent struct {
	Content     any              `json:"content"`
	Attachments []map[string]any `json:"attachments"`
}

// ── Value helpers ────────────────────────────────────────────────────────────────────────────────

func text(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func firstText(values ...any) string {
	for _, value := range values {
		if found := text(value); found != "" {
			return found
		}
	}
	return ""
}

func number(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	}
	return 0, false
}

func numberOrZero(value any) float64 {
	f, _ := number(value)
	return f
}

func object(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

func array(value any) []any {
	a, _ := value.([]any)
	return a
}

func label(value any) string {
	return text(object(value)["label"])
}

func uniqueStrings(values []string) []string {
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

func namedList(value any) []string {
	items := array(value)
	out := make([]string, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			out = append(out, firstText(v["name"], v["label"]))
		}
	}
	return uniqueStrings(out)
}

func coverURL(cover any) string {
	m := object(cover)
	return firstText(m["default"], m["md"], m["thumbnail"], m["orig"])
}

func slugOf(item map[string]any) string {
	if slug := firstText(item["slug_url"], item["slug"]); slug != "" {
		return slug
	}
	if id, ok := number(item["id"]); ok {
		return strconv.FormatFloat(id, 'f', -1, 64)
	}
	return ""
}

func bookURL(siteBase, slug string) string {
	if slug == "" {
		return ""
	}
	return strings.TrimRight(siteBase, "/") + "/ru/book/" + slug
}

// ── Normalisation ────────────────────────────────────────────────────────────────────────────────

func normalizeSearch(payload searchResponse, page int, siteBase string) (SearchResult, error) {
	if payload.Data == nil {
		return SearchResult{}, fmt.Errorf("unexpected search response")
	}
	result := SearchResult{Page: page, HasNextPage: payload.Meta.HasNextPage || payload.Links.Next != nil}
	for _, raw := range payload.Data {
		title := firstText(raw["rus_name"], raw["name"], raw["eng_name"])
		slug := slugOf(raw)
		if title == "" || slug == "" {
			continue
		}
		alternatives := uniqueStrings([]string{text(raw["eng_name"]), text(raw["name"]), text(raw["rus_name"])})
		item := SearchItem{
			Slug:              slug,
			Title:             title,
			AlternativeTitles: withoutValue(alternatives, title),
			CoverURL:          coverURL(raw["cover"]),
			Status:            label(raw["status"]),
			Type:              label(raw["type"]),
			Rating:            numberOrZero(object(raw["rating"])["average"]),
			ReleaseDate:       text(raw["releaseDateString"]),
			AgeRestriction:    label(raw["ageRestriction"]),
			URL:               bookURL(siteBase, slug),
		}
		result.Items = append(result.Items, item)
	}
	if current, ok := number(payload.Meta.CurrentPage); ok {
		result.Page = int(current)
	}
	return result, nil
}

func normalizeTitle(payload titleResponse, siteBase string) (Title, error) {
	data := payload.Data
	if data == nil {
		return Title{}, fmt.Errorf("unexpected title response")
	}
	name := firstText(data["rus_name"], data["name"], data["eng_name"])
	if name == "" {
		return Title{}, fmt.Errorf("title has no name")
	}
	slug := slugOf(data)
	alternatives := uniqueStrings([]string{text(data["eng_name"]), text(data["name"]), text(data["rus_name"])})

	return Title{
		Slug:              slug,
		Title:             name,
		AlternativeTitles: withoutValue(alternatives, name),
		Description:       documentToText(data["summary"]),
		Authors:           namedList(data["authors"]),
		Artists:           namedList(data["artists"]),
		Genres:            namedList(data["genres"]),
		Tags:              namedList(data["tags"]),
		Status:            label(data["status"]),
		Type:              label(data["type"]),
		Rating:            numberOrZero(object(data["rating"])["average"]),
		ReleaseDate:       text(data["releaseDateString"]),
		AgeRestriction:    label(data["ageRestriction"]),
		CoverURL:          coverURL(data["cover"]),
		SourceURL:         bookURL(siteBase, slug),
	}, nil
}

func withoutValue(values []string, skip string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != skip {
			out = append(out, value)
		}
	}
	return out
}

func normalizeChapters(payload chaptersResponse) ([]Chapter, error) {
	if payload.Data == nil {
		return nil, fmt.Errorf("unexpected chapter list")
	}
	merged := make(map[string]*Chapter)
	order := make([]string, 0, len(payload.Data))

	for _, raw := range payload.Data {
		branches := len(array(raw["branches"]))
		if branches == 0 {
			branches = 1
		}
		chapter := Chapter{
			Index:    numberOrZero(raw["index"]),
			Volume:   text(raw["volume"]),
			Number:   text(raw["number"]),
			Name:     text(raw["name"]),
			Branches: branches,
		}
		chapter.DisplayTitle = displayTitle(chapter)
		key := chapterKey(chapter)
		if _, ok := merged[key]; !ok {
			copied := chapter
			merged[key] = &copied
			order = append(order, key)
		}
	}

	out := make([]Chapter, 0, len(order))
	for _, key := range order {
		out = append(out, *merged[key])
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, right := sortValue(out[i]), sortValue(out[j])
		if left != right {
			return left < right
		}
		return out[i].Volume < out[j].Volume
	})
	return out, nil
}

func chapterKey(chapter Chapter) string {
	if chapter.Volume != "" || chapter.Number != "" {
		return chapter.Volume + "\x00" + chapter.Number
	}
	return fmt.Sprintf("index\x00%g", chapter.Index)
}

func displayTitle(chapter Chapter) string {
	parts := make([]string, 0, 3)
	if chapter.Volume != "" {
		parts = append(parts, "Том "+chapter.Volume)
	}
	if chapter.Number != "" {
		parts = append(parts, "Глава "+chapter.Number)
	}
	if chapter.Name != "" {
		parts = append(parts, chapter.Name)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("Глава %g", chapter.Index)
	}
	return strings.Join(parts, " ")
}

func sortValue(chapter Chapter) float64 {
	if chapter.Index != 0 {
		return chapter.Index
	}
	return numberOrZero(chapter.Volume)*1_000_000 + numberOrZero(chapter.Number)
}
