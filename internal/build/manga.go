package build

import (
	"context"
	"fmt"
	"os"

	"github.com/Mimic890/yomibako/internal/cbz"
	"github.com/Mimic890/yomibako/internal/source"
)

// ChapterOptions describes one manga chapter build.
type ChapterOptions struct {
	Source   source.MangaSource
	Title    source.Title
	Chapter  source.Chapter
	Path     string
	Progress func(format string, args ...any)
}

// ChapterResult is a written CBZ.
type ChapterResult struct {
	Path     string
	Pages    int
	Size     int64
	Warnings []string
}

// BuildChapter downloads every page of a chapter and writes it as a CBZ.
func BuildChapter(ctx context.Context, opts ChapterOptions) (ChapterResult, error) {
	progress := opts.Progress
	if progress == nil {
		progress = func(string, ...any) {}
	}

	progress("%s: страницы", opts.Chapter.DisplayTitle)
	pages, err := opts.Source.Pages(ctx, opts.Title.Slug, opts.Chapter)
	if err != nil {
		return ChapterResult{}, err
	}
	if len(pages) == 0 {
		return ChapterResult{}, fmt.Errorf("%s: нет страниц", opts.Chapter.DisplayTitle)
	}

	file, err := os.Create(opts.Path)
	if err != nil {
		return ChapterResult{}, err
	}
	defer file.Close()

	meta := cbz.Meta{
		Title:    opts.Chapter.DisplayTitle,
		Series:   opts.Title.Title,
		Number:   chapterNumber(opts.Chapter),
		Volume:   volumeNumber(opts.Chapter.Volume),
		Summary:  opts.Title.Description,
		Writer:   opts.Title.Authors,
		Genre:    opts.Title.Genres,
		Language: "ru",
		Manga:    true,
	}
	if err := cbz.Write(file, pages, meta); err != nil {
		return ChapterResult{}, err
	}
	if err := file.Close(); err != nil {
		return ChapterResult{}, err
	}

	info, err := os.Stat(opts.Path)
	if err != nil {
		return ChapterResult{}, err
	}
	return ChapterResult{Path: opts.Path, Pages: len(pages), Size: info.Size()}, nil
}

func chapterNumber(chapter source.Chapter) int {
	if n, ok := parseNumber(chapter.Number); ok {
		return n
	}
	return int(chapter.Index)
}

func parseNumber(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}
