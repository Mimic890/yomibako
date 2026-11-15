package ranobelib

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Mimic890/yomibako/internal/source"
)

// Source is the RanobeLib implementation of source.Source.
type Source struct {
	Client *Client
}

// New returns a RanobeLib source with the public defaults.
func New() *Source { return &Source{Client: NewClient()} }

// SetToken replaces the access token used for requests.
func (s *Source) SetToken(token string) { s.Client.Token = token }

// Name implements source.Source.
func (s *Source) Name() string { return "ranobelib" }

// Search implements source.Source.
func (s *Source) Search(ctx context.Context, query string, page int) (source.SearchResult, error) {
	return s.Client.Search(ctx, query, page)
}

// Title implements source.Source. A bare slug such as "lord-of-the-mysteries" is not enough for
// the API, which keys titles by "<id>--<slug>"; when the direct lookup fails the slug is resolved
// through search and the canonical one is returned in Title.Slug.
func (s *Source) Title(ctx context.Context, slug string) (source.Title, error) {
	title, err := s.Client.Title(ctx, slug)
	if err == nil {
		return title, nil
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		return source.Title{}, err
	}

	canonical, resolveErr := s.resolveSlug(ctx, slug)
	if resolveErr != nil {
		return source.Title{}, fmt.Errorf("тайтл %q не найден в API RanobeLib (возможно, лицензирован или удалён с сайта): %w", slug, err)
	}
	return s.Client.Title(ctx, canonical)
}

// resolveSlug finds the canonical "<id>--<slug>" form through search. The numeric prefix of a
// site URL ("6777--no-game-no-life-novel") is not part of the searchable name and is dropped.
func (s *Source) resolveSlug(ctx context.Context, slug string) (string, error) {
	query := strings.ReplaceAll(strings.Trim(searchName(slug), "-"), "-", " ")
	result, err := s.Client.Search(ctx, query, 1)
	if err != nil {
		return "", err
	}
	slugs := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		slugs = append(slugs, item.Slug)
	}
	return pickSlug(slugs, slug)
}

// Chapters implements source.Source.
func (s *Source) Chapters(ctx context.Context, slug string) ([]source.Chapter, error) {
	return s.Client.Chapters(ctx, slug)
}

// VolumeCovers maps a volume label to its full-resolution cover URL. The API stores the volume
// label in `info`; the `volume` field is empty for every entry seen so far.
func (s *Source) VolumeCovers(ctx context.Context, slug string) (map[string]string, error) {
	entries, err := s.Client.Covers(ctx, slug)
	if err != nil {
		return nil, err
	}
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
	return covers, nil
}

// Chapter implements source.Source: it downloads the chapter document and every image it uses,
// then renders XHTML whose image hrefs point at the local copies.
func (s *Source) Chapter(ctx context.Context, slug string, chapter source.Chapter) (source.ChapterContent, error) {
	content, err := s.Client.ChapterContent(ctx, slug, chapter)
	if err != nil {
		return source.ChapterContent{}, err
	}

	refs := collectImages(content.Content, content.Attachments)
	images := make([]source.Image, 0, len(refs))
	local := make(map[string]string, len(refs))

	for _, ref := range refs {
		image, err := s.Client.FetchImage(ctx, ref.URL)
		if err != nil {
			// A missing illustration must not fail the whole chapter.
			continue
		}
		ext := imageExtension(image.MediaType)
		if ext == "" {
			continue
		}
		name := fmt.Sprintf("img-%s.%s", shortHash(ref.URL), ext)
		image.FileName = name
		images = append(images, image)
		local[ref.URL] = "../images/" + name
	}

	html := documentToHTML(content.Content, content.Attachments, func(url string) string {
		return local[url]
	})
	text := documentToText(content.Content)
	if html == "" && text != "" {
		html = plainTextToHTML(text)
	}

	return source.ChapterContent{
		Title:  chapter.DisplayTitle,
		HTML:   html,
		Text:   strings.TrimSpace(text),
		Images: images,
	}, nil
}

// FetchImage implements source.Source.
func (s *Source) FetchImage(ctx context.Context, url string) (source.Image, error) {
	return s.Client.FetchImage(ctx, url)
}

// pickSlug chooses the canonical slug for a requested one. Exact matches win; a slug that is only
// a prefix of the real one ("no-game-no-life" against "no-game-no-life-novel") is accepted when
// exactly one result fits, so a short slug is not a dead end but an ambiguous one still is.
func pickSlug(slugs []string, want string) (string, error) {
	var exact, prefix []string
	for _, canonical := range slugs {
		name := searchName(canonical)
		switch {
		case canonical == want || name == want:
			exact = append(exact, canonical)
		case strings.HasPrefix(name, want+"-"):
			prefix = append(prefix, canonical)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) == 0 && len(prefix) > 0 {
		// Search returns the base title before its spin-offs, and the base title has the shortest
		// slug, so the shortest prefix match is the one the user meant.
		best := prefix[0]
		for _, candidate := range prefix[1:] {
			if len(candidate) < len(best) {
				best = candidate
			}
		}
		return best, nil
	}
	if len(exact) > 1 {
		return "", fmt.Errorf("slug %q is ambiguous: %s", want, strings.Join(exact, ", "))
	}
	return "", fmt.Errorf("title %q not found", want)
}

// searchName drops the numeric id prefix from a canonical slug.
func searchName(slug string) string {
	if index := strings.Index(slug, "--"); index > 0 && isDigits(slug[:index]) {
		return slug[index+2:]
	}
	return slug
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:6])
}
