package rulate

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/Mimic890/yomibako/internal/imgutil"
	"github.com/Mimic890/yomibako/internal/source"
	"github.com/Mimic890/yomibako/internal/xhtml"
)

// Source implements source.NovelSource for tl.rulate.ru.
type Source struct {
	Client *Client
}

// New returns a source with the public defaults.
func New() *Source { return &Source{Client: NewClient()} }

// NewWithBase returns a source pointing at another host, used by tests.
func NewWithBase(base string) *Source {
	client := NewClient()
	client.Base = strings.TrimRight(base, "/")
	return &Source{Client: client}
}

// Name implements source.Source.
func (s *Source) Name() string { return "rulate" }

// Kind implements source.Source.
func (s *Source) Kind() source.Kind { return source.KindNovel }

// SetToken is a no-op: Rulate authorises through cookies.
func (s *Source) SetToken(string) {}

// SetCookies implements the registry's cookie hook.
func (s *Source) SetCookies(value string) { s.Client.SetCookies(value) }

// Search implements source.Source through the site's own search page.
func (s *Source) Search(ctx context.Context, query string, page int) (source.SearchResult, error) {
	target := fmt.Sprintf("%s/search?q=%s&page=%d", s.Client.Base, strings.ReplaceAll(query, " ", "+"), page)
	body, err := s.Client.get(ctx, target, "text/html")
	if err != nil {
		return source.SearchResult{}, err
	}

	items := make([]source.SearchItem, 0, 10)
	for _, match := range regexpBookLink.FindAllStringSubmatch(string(body), 40) {
		id, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		items = append(items, source.SearchItem{
			Slug:  strconv.Itoa(id),
			Title: strings.TrimSpace(html.UnescapeString(tagPattern.ReplaceAllString(match[2], ""))),
			URL:   fmt.Sprintf("%s/book/%d", s.Client.Base, id),
		})
		if len(items) == 10 {
			break
		}
	}
	return source.SearchResult{Items: items, Page: page}, nil
}

// Title implements source.Source by reading the meta tags of the book page.
func (s *Source) Title(ctx context.Context, slug string) (source.Title, error) {
	id := bookID(slug)
	if id == 0 {
		return source.Title{}, fmt.Errorf("rulate: не разобрал ссылку %q", slug)
	}

	body, err := s.Client.get(ctx, fmt.Sprintf("%s/book/%d", s.Client.Base, id), "text/html")
	if err != nil {
		return source.Title{}, err
	}
	document := string(body)

	title := html.UnescapeString(meta(ogTitlePattern, document))
	if title == "" {
		return source.Title{}, fmt.Errorf("rulate: страница книги %d без названия", id)
	}
	description := html.UnescapeString(meta(ogDescPattern, document))
	cover := meta(ogCoverPattern, document)
	if strings.HasPrefix(cover, "/") {
		cover = s.Client.Base + cover
	}

	authors := make([]string, 0, 1)
	if match := authorPattern.FindStringSubmatch(document); match != nil {
		name := strings.TrimSpace(html.UnescapeString(match[1]))
		if name != "" && !strings.EqualFold(name, "автор") {
			authors = append(authors, name)
		}
	}

	return source.Title{
		Slug:        strconv.Itoa(id),
		Title:       title,
		Description: description,
		Authors:     authors,
		CoverURL:    cover,
		SourceURL:   fmt.Sprintf("%s/book/%d", s.Client.Base, id),
	}, nil
}

func meta(pattern *regexp.Regexp, document string) string {
	if match := pattern.FindStringSubmatch(document); match != nil {
		return strings.TrimSpace(match[1])
	}
	return ""
}

// Chapters implements source.Source. The site answers with a JSON list of every chapter, so the
// volume has to come from the chapter title, which is what the translator wrote it into.
func (s *Source) Chapters(ctx context.Context, slug string) ([]source.Chapter, error) {
	id := bookID(slug)
	if id == 0 {
		return nil, fmt.Errorf("rulate: не разобрал ссылку %q", slug)
	}

	body, err := s.Client.get(ctx, fmt.Sprintf("%s/book/%d/chapters", s.Client.Base, id), "application/json")
	if err != nil {
		return nil, err
	}

	var entries []chapterEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("rulate: не разобрал список глав книги %d: %w", id, err)
	}

	chapters := make([]source.Chapter, 0, len(entries))
	for index, entry := range entries {
		name := strings.TrimSpace(html.UnescapeString(entry.Title))
		chapter := source.Chapter{
			Index:  float64(index + 1),
			Volume: volumeOf(name, index),
			Number: strconv.Itoa(index + 1),
			Name:   name,
			Ref:    strconv.Itoa(entry.ID),
		}
		chapter.DisplayTitle = name
		if chapter.DisplayTitle == "" {
			chapter.DisplayTitle = fmt.Sprintf("Глава %d", index+1)
		}
		chapters = append(chapters, chapter)
	}
	return chapters, nil
}

// volumeOf reads "Том 3" out of a chapter title. Rulate keeps no volume column, so books whose
// titles carry no volume end up without one, which the builder refuses rather than guessing.
func volumeOf(title string, index int) string {
	if match := volumePattern.FindStringSubmatch(title); match != nil {
		return match[1]
	}
	return ""
}

// Chapter implements source.NovelSource: it fetches the reader page and keeps only the body.
func (s *Source) Chapter(ctx context.Context, slug string, chapter source.Chapter) (source.ChapterContent, error) {
	id := bookID(slug)
	chapterID, err := strconv.Atoi(strings.TrimSpace(chapter.Ref))
	if err != nil || chapterID == 0 {
		return source.ChapterContent{}, fmt.Errorf("rulate: у главы %q нет идентификатора", chapter.DisplayTitle)
	}

	body, err := s.Client.get(ctx, fmt.Sprintf("%s/book/%d/%d/ready", s.Client.Base, id, chapterID), "text/html")
	if err != nil {
		return source.ChapterContent{}, err
	}
	document := string(body)

	inner := xhtml.InnerHTML(document, "content-text")
	if strings.TrimSpace(inner) == "" {
		if strings.Contains(document, "Купить") || strings.Contains(document, "оплатить") {
			return source.ChapterContent{}, fmt.Errorf("rulate: глава %q платная, нужны куки аккаунта", chapter.DisplayTitle)
		}
		return source.ChapterContent{}, fmt.Errorf("rulate: не нашёл текст главы %q", chapter.DisplayTitle)
	}

	var images []source.Image
	names := make(map[string]string, 4)
	markup := xhtml.FromHTML(inner, xhtml.Options{
		ResolveImage: func(src string) string {
			abs := src
			if strings.HasPrefix(src, "/") {
				abs = s.Client.Base + src
			}
			if _, ok := names[abs]; !ok {
				image, err := s.Client.FetchImage(ctx, abs)
				if err != nil {
					return ""
				}
				ext := imgutil.Extension(image.MediaType)
				if ext == "" {
					return ""
				}
				name := fmt.Sprintf("img-%s.%s", imgutil.Hash(abs), ext)
				image.FileName = name
				images = append(images, image)
				names[abs] = name
			}
			return "../images/" + names[abs]
		},
	})
	if strings.TrimSpace(markup) == "" {
		return source.ChapterContent{}, fmt.Errorf("rulate: глава %q пустая", chapter.DisplayTitle)
	}

	return source.ChapterContent{
		Title:  chapter.DisplayTitle,
		HTML:   markup,
		Text:   xhtml.Text(inner),
		Images: images,
	}, nil
}

// VolumeCovers implements source.NovelSource: Rulate has one cover per book.
func (s *Source) VolumeCovers(context.Context, string) (map[string]string, error) {
	return map[string]string{}, nil
}

// FetchImage implements source.Source.
func (s *Source) FetchImage(ctx context.Context, rawURL string) (source.Image, error) {
	return s.Client.FetchImage(ctx, rawURL)
}
