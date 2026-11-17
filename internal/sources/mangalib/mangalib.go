// Package mangalib reads manga and manhwa from the cdnlibs family of sites: MangaLib and
// HentaiLib share one API and differ only in the site id and the site the requests come from.
package mangalib

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/Mimic890/yomibako/internal/source"
	"github.com/Mimic890/yomibako/internal/sources/ranobelib"
)

// Site identifiers used by the shared API.
const (
	MangaLibSiteID  = 1
	HentaiLibSiteID = 4
)

// Source is a manga site backed by the cdnlibs API.
type Source struct {
	Client *ranobelib.Client
	name   string
}

// New returns the MangaLib source.
func New() *Source {
	client := ranobelib.NewClient()
	client.SiteID = MangaLibSiteID
	client.SiteBase = "https://mangalib.me"
	return &Source{Client: client, name: "mangalib"}
}

// NewHentaiLib returns the HentaiLib source.
func NewHentaiLib() *Source {
	client := ranobelib.NewClient()
	client.SiteID = HentaiLibSiteID
	client.SiteBase = "https://hentailib.me"
	return &Source{Client: client, name: "hentailib"}
}

// VolumeCovers maps a volume label to its full-resolution cover, the same way RanobeLib does.
func (s *Source) VolumeCovers(ctx context.Context, slug string) (map[string]string, error) {
	entries, err := s.Client.Covers(ctx, slug)
	if err != nil {
		return nil, err
	}
	return ranobelib.CoverMap(entries), nil
}

// SetCookies replaces the Cookie header sent with every request.
func (s *Source) SetCookies(value string) { s.Client.Cookies = value }

// Name implements source.Source.
func (s *Source) Name() string { return s.name }

// Kind implements source.Source.
func (s *Source) Kind() source.Kind { return source.KindManga }

// SetToken replaces the access token used for requests.
func (s *Source) SetToken(token string) { s.Client.Token = token }

// Search implements source.Source.
func (s *Source) Search(ctx context.Context, query string, page int) (source.SearchResult, error) {
	return s.Client.Search(ctx, query, page)
}

// Title implements source.Source.
func (s *Source) Title(ctx context.Context, slug string) (source.Title, error) {
	slug = ranobelib.NormalizeSlug(slug)
	return s.Client.Title(ctx, slug)
}

// Chapters implements source.Source.
func (s *Source) Chapters(ctx context.Context, slug string) ([]source.Chapter, error) {
	return s.Client.Chapters(ctx, slug)
}

// FetchImage implements source.Source.
func (s *Source) FetchImage(ctx context.Context, url string) (source.Image, error) {
	return s.Client.FetchImage(ctx, url)
}

// Pages implements source.MangaSource. Pages are downloaded four at a time and kept in order; a
// chapter with a missing page is rejected rather than written with a gap.
func (s *Source) Pages(ctx context.Context, slug string, chapter source.Chapter) ([]source.Image, error) {
	refs, err := s.Client.ChapterPages(ctx, slug, chapter)
	if err != nil {
		// The cdnlibs API answers "not found" instead of "forbidden" for chapters it will not show
		// without a session, which is the usual case for 18+ titles.
		var apiErr *ranobelib.Error
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return nil, fmt.Errorf("глава %s: сайт не отдаёт страницы без входа - положи куки от %s в cookies.txt (%w)",
				chapter.DisplayTitle, strings.TrimPrefix(s.Client.SiteBase, "https://"), err)
		}
		return nil, err
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("глава без страниц")
	}

	const workers = 4
	type result struct {
		index int
		image source.Image
		err   error
	}

	jobs := make(chan int)
	results := make(chan result, len(refs))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				image, err := s.Client.FetchImage(ctx, refs[index].URL)
				results <- result{index: index, image: image, err: err}
			}
		}()
	}
	go func() {
		for i := range refs {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	images := make([]source.Image, len(refs))
	for item := range results {
		if item.err != nil {
			return nil, fmt.Errorf("страница %d: %w", item.index+1, item.err)
		}
		images[item.index] = item.image
	}
	return images, nil
}
