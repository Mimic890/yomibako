package build

import (
	"context"
	"fmt"
	"os"

	"github.com/Mimic890/yomibako/internal/cbz"
	"github.com/Mimic890/yomibako/internal/source"
)

// VolumeOptions describes one manga volume build.
type VolumeOptions struct {
	Source source.MangaSource
	Title  source.Title
	Volume Volume
	// CoverURL is the volume's own poster, if the source has one. It goes in as the first page so
	// BookOrbit picks it up as the book cover.
	CoverURL string
	Path     string
	Progress func(format string, args ...any)
	// OnChapter reports the chapter being built, so a queue can show progress inside a volume.
	OnChapter func(done, total int)
}

// VolumeResult is a written CBZ.
type VolumeResult struct {
	Path     string
	Pages    int
	Chapters int
	Size     int64
}

// BuildComicVolume downloads every page of every chapter of a volume and writes one CBZ. BookOrbit makes
// one book out of one file, so a volume has to be a single archive to be a single entry with its own
// poster; chapters stay apart only inside the archive.
func BuildComicVolume(ctx context.Context, opts VolumeOptions) (VolumeResult, error) {
	progress := opts.Progress
	if progress == nil {
		progress = func(string, ...any) {}
	}
	if len(opts.Volume.Chapters) == 0 {
		return VolumeResult{}, fmt.Errorf("%s: в томе нет глав", VolumeTitle(opts.Volume.Label))
	}

	// The cover is the first page: BookOrbit takes the first image of a CBZ as the book cover, and
	// a volume poster in front of the pages reads naturally too.
	pages := make([]source.Image, 0, 200)
	if opts.CoverURL != "" {
		if cover, err := opts.Source.FetchImage(ctx, opts.CoverURL); err == nil {
			pages = append(pages, cover)
		}
	}

	for index, chapter := range opts.Volume.Chapters {
		if err := ctx.Err(); err != nil {
			return VolumeResult{}, err
		}
		progress("%s: %s", VolumeTitle(opts.Volume.Label), chapter.DisplayTitle)
		if opts.OnChapter != nil {
			opts.OnChapter(index+1, len(opts.Volume.Chapters))
		}
		chapterPages, err := opts.Source.Pages(ctx, opts.Title.Slug, chapter)
		if err != nil {
			return VolumeResult{}, fmt.Errorf("%s: %w", chapter.DisplayTitle, err)
		}
		pages = append(pages, chapterPages...)
	}
	if len(pages) == 0 {
		return VolumeResult{}, fmt.Errorf("%s: нет страниц", VolumeTitle(opts.Volume.Label))
	}

	file, err := os.Create(opts.Path)
	if err != nil {
		return VolumeResult{}, err
	}
	defer file.Close()

	meta := cbz.Meta{
		Title:    VolumeTitle(opts.Volume.Label),
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
		Chapters: len(opts.Volume.Chapters),
		Size:     info.Size(),
	}, nil
}
