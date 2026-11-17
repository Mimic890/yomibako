// Package source defines the contract every book source implements.
//
// A source is a website the service can read titles from. Each site gets its own package under
// internal/sources; the builder and the CLI only ever talk to this interface, so adding a site
// means adding a package and registering it, not touching the EPUB writer.
package source

import "context"

// SearchItem is one row of a search response.
type SearchItem struct {
	Slug              string
	Title             string
	AlternativeTitles []string
	CoverURL          string
	Status            string
	Type              string
	Rating            float64
	ReleaseDate       string
	AgeRestriction    string
	URL               string
}

// SearchResult is a page of search results.
type SearchResult struct {
	Items       []SearchItem
	Page        int
	HasNextPage bool
}

// Title is a full title card.
type Title struct {
	Slug              string
	Title             string
	AlternativeTitles []string
	Description       string
	Authors           []string
	Artists           []string
	Genres            []string
	Tags              []string
	Status            string
	Type              string
	Rating            float64
	ReleaseDate       string
	AgeRestriction    string
	CoverURL          string
	SourceURL         string
}

// Chapter is one chapter entry. Volume is the source's own volume label and may be empty.
type Chapter struct {
	Index        float64
	Volume       string
	Number       string
	Name         string
	DisplayTitle string
	BranchID     int
	// Ref is the source's own identifier for the chapter, used when fetching its body.
	Ref string
	// Branches is how many translation variants the source lists for this chapter. The chapter
	// itself is one, but readers that list every variant show more entries than there are chapters.
	Branches int
}

// Image is a downloaded image ready to be embedded into an EPUB.
type Image struct {
	URL       string
	FileName  string
	MediaType string
	Bytes     []byte
}

// ChapterContent is a rendered chapter: XHTML with local image hrefs plus the images themselves.
type ChapterContent struct {
	Title  string
	HTML   string
	Text   string
	Images []Image
}

// Kind tells the builder what a source produces: text chapters or image pages.
type Kind string

// Source kinds.
const (
	KindNovel Kind = "novel"
	KindManga Kind = "manga"
)

// Source is a website the service can read from.
type Source interface {
	// Name is the stable identifier used in config and state, for example "ranobelib".
	Name() string
	// Kind is what the source produces.
	Kind() Kind
	// Search returns one page of results for a query.
	Search(ctx context.Context, query string, page int) (SearchResult, error)
	// Title returns the full card for a slug.
	Title(ctx context.Context, slug string) (Title, error)
	// Chapters returns every chapter of a title, sorted by the source's own index.
	Chapters(ctx context.Context, slug string) ([]Chapter, error)
	// FetchImage downloads an image and detects its media type.
	FetchImage(ctx context.Context, url string) (Image, error)
}

// NovelSource is a source of text chapters, built into EPUB volumes.
type NovelSource interface {
	Source
	// Chapter returns the rendered content of one chapter.
	Chapter(ctx context.Context, slug string, chapter Chapter) (ChapterContent, error)
	// VolumeCovers maps a volume label to a full-resolution cover URL.
	VolumeCovers(ctx context.Context, slug string) (map[string]string, error)
}

// CoverSource is a source that has a separate poster for every volume.
type CoverSource interface {
	VolumeCovers(ctx context.Context, slug string) (map[string]string, error)
}

// MangaSource is a source of image pages, built into one CBZ per chapter.
type MangaSource interface {
	Source
	// Pages returns the page images of one chapter, in reading order.
	Pages(ctx context.Context, slug string, chapter Chapter) ([]Image, error)
}
