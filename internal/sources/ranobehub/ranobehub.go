package ranobehub

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Mimic890/yomibako/internal/imgutil"
	"github.com/Mimic890/yomibako/internal/source"
	"github.com/Mimic890/yomibako/internal/xhtml"
)

// Source implements source.NovelSource for ranobehub.org.
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
func (s *Source) Name() string { return "ranobehub" }

// Kind implements source.Source.
func (s *Source) Kind() source.Kind { return source.KindNovel }

// SetToken is a no-op: RanobeHub authorises through cookies.
func (s *Source) SetToken(string) {}

// SetCookies implements the registry's cookie hook.
func (s *Source) SetCookies(value string) { s.Client.SetCookies(value) }

var (
	h1Pattern     = regexp.MustCompile(`(?is)<h1[^>]*>([\s\S]*?)</h1>`)
	descPattern   = regexp.MustCompile(`(?is)id="book-description"[^>]*>([\s\S]*?)</div>`)
	coverPattern  = regexp.MustCompile(`/_next/image\?url=([^"&]+)`)
	authorPattern = regexp.MustCompile(`(?is)book-author-byline[\s\S]{0,400}?<a[^>]*>([\s\S]{0,200}?)</a>`)
	tagPattern    = regexp.MustCompile(`<[^>]+>`)
)

// Search implements source.Source.
func (s *Source) Search(ctx context.Context, query string, page int) (source.SearchResult, error) {
	target := fmt.Sprintf("%s/api/search?q=%s", s.Client.Base, url.QueryEscape(query))
	var response searchResponse
	if err := s.Client.getJSON(ctx, target, &response); err != nil {
		return source.SearchResult{}, err
	}

	items := make([]source.SearchItem, 0, len(response.Books))
	for _, book := range response.Books {
		slug := book.Slug
		if slug == "" {
			slug = strconv.Itoa(book.ID)
		} else {
			slug = fmt.Sprintf("%d-%s", book.ID, bookslug(book.Slug))
		}
		items = append(items, source.SearchItem{
			Slug:     slug,
			Title:    book.Title,
			CoverURL: s.Client.Base + book.PosterURL,
			URL:      s.Client.Base + "/ranobe/" + slug,
		})
	}
	return source.SearchResult{Items: items, Page: page}, nil
}

// bookslug keeps only the part that belongs in a page path.
func bookslug(slug string) string {
	if index := strings.Index(slug, "/"); index >= 0 {
		return slug[index+1:]
	}
	return slug
}

// Title implements source.Source by reading the card from the page.
func (s *Source) Title(ctx context.Context, slug string) (source.Title, error) {
	path, id := normalizeSlug(slug)
	if id == 0 {
		return source.Title{}, fmt.Errorf("ranobehub: не разобрал ссылку %q", slug)
	}

	page, err := s.Client.get(ctx, s.Client.Base+"/ranobe/"+path, "text/html")
	if err != nil {
		return source.Title{}, err
	}
	document := string(page)

	title := ""
	if match := h1Pattern.FindStringSubmatch(document); match != nil {
		title = strings.TrimSpace(tagPattern.ReplaceAllString(match[1], ""))
	}
	description := ""
	if match := descPattern.FindStringSubmatch(document); match != nil {
		description = xhtml.Text(match[1])
	}
	cover := ""
	if match := coverPattern.FindStringSubmatch(document); match != nil {
		if decoded, err := url.QueryUnescape(match[1]); err == nil {
			cover = s.Client.Base + decoded
		}
	}
	authors := make([]string, 0, 1)
	if match := authorPattern.FindStringSubmatch(document); match != nil {
		if name := strings.TrimSpace(tagPattern.ReplaceAllString(match[1], "")); name != "" {
			authors = append(authors, name)
		}
	}
	if title == "" {
		return source.Title{}, fmt.Errorf("ranobehub: страница %s без названия", path)
	}

	return source.Title{
		Slug:        path,
		Title:       title,
		Description: description,
		Authors:     authors,
		CoverURL:    cover,
		SourceURL:   s.Client.Base + "/ranobe/" + path,
	}, nil
}

// Chapters implements source.Source. The API pages through chapters 250 at a time.
func (s *Source) Chapters(ctx context.Context, slug string) ([]source.Chapter, error) {
	_, id := normalizeSlug(slug)
	if id == 0 {
		return nil, fmt.Errorf("ranobehub: не разобрал ссылку %q", slug)
	}

	const pageSize = 250
	var items []chapterItem
	for offset := 0; ; offset += pageSize {
		target := fmt.Sprintf("%s/api/books/%d/chapters?limit=%d&offset=%d", s.Client.Base, id, pageSize, offset)
		var response chaptersResponse
		if err := s.Client.getJSON(ctx, target, &response); err != nil {
			return nil, err
		}
		items = append(items, response.Items...)
		if len(response.Items) < pageSize || (response.Total > 0 && len(items) >= response.Total) {
			break
		}
	}

	chapters := make([]source.Chapter, 0, len(items))
	for _, item := range items {
		volume := ""
		if item.Volume > 0 {
			volume = strconv.Itoa(item.Volume)
		}
		chapter := source.Chapter{
			Index:  float64(item.Number),
			Volume: volume,
			Number: strconv.Itoa(item.Number),
			Name:   item.Title,
			Ref:    strconv.Itoa(item.ID),
		}
		chapter.DisplayTitle = displayTitle(volume, item.Number, item.Title)
		chapters = append(chapters, chapter)
	}
	return chapters, nil
}

func displayTitle(volume string, number int, name string) string {
	switch {
	case volume != "" && number > 0:
		return fmt.Sprintf("Том %s Глава %d %s", volume, number, name)
	case number > 0:
		return fmt.Sprintf("Глава %d %s", number, name)
	}
	return name
}

// chapterID digs the chapter id out of the value stored when the list was read.
func chapterID(chapter source.Chapter) int {
	if id, err := strconv.Atoi(strings.TrimSpace(chapter.Ref)); err == nil && id > 0 {
		return id
	}
	return 0
}

// Chapter implements source.NovelSource.
func (s *Source) Chapter(ctx context.Context, slug string, chapter source.Chapter) (source.ChapterContent, error) {
	id := chapterID(chapter)
	if id == 0 {
		return source.ChapterContent{}, fmt.Errorf("ranobehub: у главы %q нет идентификатора", chapter.DisplayTitle)
	}

	var response chapterResponse
	if err := s.Client.getJSON(ctx, fmt.Sprintf("%s/api/chapters/%d", s.Client.Base, id), &response); err != nil {
		return source.ChapterContent{}, err
	}

	body := response.Chapter.HTML
	body = regexp.MustCompile(`src="/(api/media/[^"]*)"`).ReplaceAllString(body, `src="$1"`)

	var images []source.Image
	names := make(map[string]string, 8)

	html := xhtml.FromHTML(body, xhtml.Options{
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

	if strings.TrimSpace(html) == "" {
		return source.ChapterContent{}, fmt.Errorf("ranobehub: глава %q пустая", chapter.DisplayTitle)
	}
	return source.ChapterContent{Title: chapter.DisplayTitle, HTML: html, Text: xhtml.Text(body), Images: images}, nil
}

// VolumeCovers implements source.NovelSource: RanobeHub keeps one cover per title.
func (s *Source) VolumeCovers(context.Context, string) (map[string]string, error) {
	return map[string]string{}, nil
}

// FetchImage implements source.Source.
func (s *Source) FetchImage(ctx context.Context, rawURL string) (source.Image, error) {
	return s.Client.FetchImage(ctx, rawURL)
}
