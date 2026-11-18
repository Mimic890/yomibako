package build

import (
	"context"
	"fmt"
	"html"
	"os"
	"strings"

	"github.com/Mimic890/yomibako/internal/cbz"
	"github.com/Mimic890/yomibako/internal/epub"
	"github.com/Mimic890/yomibako/internal/imgutil"
	"github.com/Mimic890/yomibako/internal/source"
)

// VolumeOptions describes one manga build.
type VolumeOptions struct {
	Source source.MangaSource
	Title  source.Title
	Volume Volume
	// CoverURL is the volume's own poster, if the source has one. It goes in as the first page so
	// BookOrbit picks it up as the book cover.
	CoverURL string
	Path     string
	Progress func(format string, args ...any)
	// CoverPath is a local image file used as the volume cover when the source has none.
	CoverPath string
	// Format is the file to write: cbz (the default) or epub.
	Format string
	// OnChapter reports the chapter being built, so a queue can show progress inside a volume.
	OnChapter func(done, total int)
}

// VolumeResult is a written file.
type VolumeResult struct {
	Path     string
	Pages    int
	Chapters int
	Size     int64
	Format   string
}

// comicSection is one chapter worth of pages.
type comicSection struct {
	Title string
	Pages []source.Image
}

// BuildComicVolume downloads every page of every chapter of a volume and writes one file. BookOrbit
// makes one book out of one file, so a volume has to be a single archive to be a single entry with
// its own poster; chapters stay apart only inside the archive.
func BuildComicVolume(ctx context.Context, opts VolumeOptions) (VolumeResult, error) {
	progress := opts.Progress
	if progress == nil {
		progress = func(string, ...any) {}
	}
	if len(opts.Volume.Chapters) == 0 {
		return VolumeResult{}, fmt.Errorf("%s: в томе нет глав", Display(opts.Volume))
	}

	// The cover is the first page: BookOrbit takes the first image of a CBZ as the book cover, and
	// a volume poster in front of the pages reads naturally too.
	var cover source.Image
	if opts.CoverPath != "" {
		if body, err := os.ReadFile(opts.CoverPath); err == nil && len(body) > 0 {
			mediaType := imgutil.Detect(body)
			cover = source.Image{
				URL:       opts.CoverPath,
				FileName:  "cover." + imgutil.Extension(mediaType),
				MediaType: mediaType,
				Bytes:     body,
			}
		}
	}
	if cover.Bytes == nil && opts.CoverURL != "" {
		if fetched, err := opts.Source.FetchImage(ctx, opts.CoverURL); err == nil {
			cover = fetched
		}
	}

	sections := make([]comicSection, 0, len(opts.Volume.Chapters))
	for index, chapter := range opts.Volume.Chapters {
		if err := ctx.Err(); err != nil {
			return VolumeResult{}, err
		}
		progress("%s: %s", Display(opts.Volume), chapter.DisplayTitle)
		if opts.OnChapter != nil {
			opts.OnChapter(index+1, len(opts.Volume.Chapters))
		}
		pages, err := opts.Source.Pages(ctx, opts.Title.Slug, chapter)
		if err != nil {
			return VolumeResult{}, fmt.Errorf("%s: %w", chapter.DisplayTitle, err)
		}
		if len(pages) > 0 {
			sections = append(sections, comicSection{Title: chapter.DisplayTitle, Pages: pages})
		}
	}
	if len(sections) == 0 {
		return VolumeResult{}, fmt.Errorf("%s: нет страниц", Display(opts.Volume))
	}

	switch opts.Format {
	case FormatEPUB:
		return writeComicEPUB(opts, cover, sections)
	default:
		return writeComicCBZ(opts, cover, sections)
	}
}

func writeComicCBZ(opts VolumeOptions, cover source.Image, sections []comicSection) (VolumeResult, error) {
	total := 0
	for _, section := range sections {
		total += len(section.Pages)
	}
	pages := make([]source.Image, 0, total+1)
	if cover.Bytes != nil {
		pages = append(pages, cover)
	}
	for _, section := range sections {
		pages = append(pages, section.Pages...)
	}

	file, err := os.Create(opts.Path)
	if err != nil {
		return VolumeResult{}, err
	}
	defer file.Close()

	meta := cbz.Meta{
		Title:    Display(opts.Volume),
		Series:   opts.Title.Title,
		Number:   opts.Volume.Number,
		Volume:   opts.Volume.Number,
		Summary:  opts.Title.Description,
		Writer:   opts.Title.Authors,
		Genre:    opts.Title.Genres,
		Language: "ru",
		Manga:    true,
	}
	if err := cbz.Write(file, pages, meta); err != nil {
		return VolumeResult{}, err
	}
	if err := file.Close(); err != nil {
		return VolumeResult{}, err
	}

	info, err := os.Stat(opts.Path)
	if err != nil {
		return VolumeResult{}, err
	}
	return VolumeResult{
		Path:     opts.Path,
		Pages:    len(pages),
		Chapters: len(sections),
		Size:     info.Size(),
		Format:   FormatCBZ,
	}, nil
}

// writeComicEPUB writes the same pages as an EPUB: one chapter per source chapter, one picture per
// page. readers that prefer EPUB get the same book, and BookOrbit still shows the cover.
func writeComicEPUB(opts VolumeOptions, cover source.Image, sections []comicSection) (VolumeResult, error) {
	images := make([]epub.Image, 0, 256)
	chapters := make([]epub.Chapter, 0, len(sections))
	index := 0

	for _, section := range sections {
		var body strings.Builder
		for _, page := range section.Pages {
			ext := imgutil.Extension(page.MediaType)
			if ext == "" {
				continue
			}
			index++
			name := fmt.Sprintf("%03d.%s", index, ext)
			images = append(images, epub.Image{FileName: name, MediaType: page.MediaType, Bytes: page.Bytes})
			fmt.Fprintf(&body, `<p><img src="../images/%s" alt="%s"/></p>`+"\n", name, html.EscapeString(section.Title))
		}
		if body.Len() == 0 {
			continue
		}
		chapters = append(chapters, epub.Chapter{Title: section.Title, HTML: body.String()})
	}
	if len(chapters) == 0 {
		return VolumeResult{}, fmt.Errorf("%s: нет страниц", Display(opts.Volume))
	}

	var bookCover *epub.Image
	if cover.Bytes != nil {
		if ext := imgutil.Extension(cover.MediaType); ext != "" {
			bookCover = &epub.Image{FileName: "cover." + ext, MediaType: cover.MediaType, Bytes: cover.Bytes}
		}
	}

	data, err := epub.Build(epub.Book{
		Metadata: epub.Metadata{
			Title:       Display(opts.Volume),
			Series:      opts.Title.Title,
			SeriesIndex: opts.Volume.Number,
			Authors:     opts.Title.Authors,
			Description: opts.Title.Description,
			Genres:      opts.Title.Genres,
			Tags:        opts.Title.Tags,
			SourceURL:   opts.Title.SourceURL,
			Language:    "ru",
		},
		Chapters: chapters,
		Cover:    bookCover,
		Images:   images,
	})
	if err != nil {
		return VolumeResult{}, err
	}
	if err := os.WriteFile(opts.Path, data, 0o644); err != nil {
		return VolumeResult{}, err
	}
	return VolumeResult{
		Path:     opts.Path,
		Pages:    index,
		Chapters: len(chapters),
		Size:     int64(len(data)),
		Format:   FormatEPUB,
	}, nil
}
