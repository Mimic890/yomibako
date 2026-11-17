// Package build turns a source's chapters into EPUB files, one volume at a time.
package build

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Mimic890/yomibako/internal/epub"
	"github.com/Mimic890/yomibako/internal/source"
)

// Volume is a group of chapters that becomes one EPUB.
type Volume struct {
	Label    string
	Number   int
	Chapters []source.Chapter
}

// GroupVolumes splits chapters by the source's own volume label. Chapters without a volume go into
// a group labelled "0", which is rendered as "Без тома".
func GroupVolumes(chapters []source.Chapter) []Volume {
	byLabel := make(map[string]*Volume)
	order := make([]string, 0, 4)

	for _, chapter := range chapters {
		label := strings.TrimSpace(chapter.Volume)
		if label == "" {
			label = "0"
		}
		group, ok := byLabel[label]
		if !ok {
			group = &Volume{Label: label, Number: volumeNumber(label)}
			byLabel[label] = group
			order = append(order, label)
		}
		group.Chapters = append(group.Chapters, chapter)
	}

	out := make([]Volume, 0, len(order))
	for _, label := range order {
		out = append(out, *byLabel[label])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

func volumeNumber(label string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(label)); err == nil {
		return n
	}
	return 0
}

// ErrNoVolumes is returned for a title whose source does not split chapters into volumes. The
// service will not guess where a volume ends: such a title has to wait for an explicit rule.
var ErrNoVolumes = errors.New("у этого источника нет деления на тома, так собирать пока нельзя")

// RequireVolumes rejects a single unnamed group, which is what a source without volumes produces.
func RequireVolumes(volumes []Volume) error {
	if len(volumes) == 0 {
		return errors.New("у тайтла нет глав")
	}
	if len(volumes) == 1 && volumes[0].Label == "0" {
		return ErrNoVolumes
	}
	return nil
}

// ChunkVolumes splits chapters into groups of size when a source keeps no volumes of its own, which
// is the normal case for comics sites. Ten chapters is a rough manga volume.
func ChunkVolumes(chapters []source.Chapter, size int) []Volume {
	if size <= 0 {
		size = 10
	}
	volumes := make([]Volume, 0, len(chapters)/size+1)
	for start := 0; start < len(chapters); start += size {
		end := start + size
		if end > len(chapters) {
			end = len(chapters)
		}
		number := len(volumes) + 1
		volumes = append(volumes, Volume{
			Label:    strconv.Itoa(number),
			Number:   number,
			Chapters: chapters[start:end],
		})
	}
	return volumes
}

// PlanVolumes groups chapters into volumes, falling back to fixed-size chunks for a comic source
// that has no volume data. Novels stay strict: an EPUB without volumes is refused, not guessed.
func PlanVolumes(kind source.Kind, chapters []source.Chapter) ([]Volume, error) {
	volumes := GroupVolumes(chapters)
	if err := RequireVolumes(volumes); err == nil {
		return volumes, nil
	}
	if kind != source.KindManga {
		return nil, ErrNoVolumes
	}
	return ChunkVolumes(chapters, 10), nil
}

// Variants counts every translation variant of every chapter, which is the number a reader that
// lists each variant shows.
func Variants(chapters []source.Chapter) int {
	total := 0
	for _, chapter := range chapters {
		if chapter.Branches > 1 {
			total += chapter.Branches
		} else {
			total++
		}
	}
	return total
}

// VolumeTitle is the human-readable name of a volume.
func VolumeTitle(label string) string {
	if label == "" || label == "0" {
		return "Без тома"
	}
	return "Том " + label
}

// Options controls one volume build.
type Options struct {
	Source        source.NovelSource
	Title         source.Title
	Volume        Volume
	CoverURL      string
	IncludeImages bool
	Progress      func(format string, args ...any)
}

// Result is a built volume.
type Result struct {
	Bytes    []byte
	Warnings []string
}

// BuildVolume downloads every chapter of a volume and renders it as an EPUB.
func BuildVolume(ctx context.Context, opts Options) (Result, error) {
	progress := opts.Progress
	if progress == nil {
		progress = func(string, ...any) {}
	}

	chapters := make([]epub.Chapter, 0, len(opts.Volume.Chapters))
	images := make([]epub.Image, 0, 16)
	seenImages := make(map[string]struct{})
	var warnings []string

	for i, chapter := range opts.Volume.Chapters {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		progress("[%d/%d] %s", i+1, len(opts.Volume.Chapters), chapter.DisplayTitle)

		content, err := opts.Source.Chapter(ctx, opts.Title.Slug, chapter)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", chapter.DisplayTitle, err))
			continue
		}
		if strings.TrimSpace(content.HTML) == "" {
			warnings = append(warnings, fmt.Sprintf("%s: пустая глава", chapter.DisplayTitle))
			continue
		}

		chapters = append(chapters, epub.Chapter{Title: content.Title, HTML: content.HTML})

		if !opts.IncludeImages {
			continue
		}
		for _, image := range content.Images {
			if _, ok := seenImages[image.FileName]; ok {
				continue
			}
			seenImages[image.FileName] = struct{}{}
			images = append(images, epub.Image{FileName: image.FileName, MediaType: image.MediaType, Bytes: image.Bytes})
		}
	}

	if len(chapters) == 0 {
		return Result{}, fmt.Errorf("volume %s: no chapters could be downloaded", VolumeTitle(opts.Volume.Label))
	}

	var cover *epub.Image
	if opts.CoverURL != "" {
		image, err := opts.Source.FetchImage(ctx, opts.CoverURL)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("обложка: %v", err))
		} else {
			cover = &epub.Image{MediaType: image.MediaType, Bytes: image.Bytes}
		}
	}

	book := epub.Book{
		Metadata: epub.Metadata{
			Title:       opts.Title.Title,
			Series:      opts.Title.Title,
			SeriesIndex: opts.Volume.Number,
			Authors:     opts.Title.Authors,
			Description: opts.Title.Description,
			Genres:      opts.Title.Genres,
			Tags:        opts.Title.Tags,
			SourceURL:   opts.Title.SourceURL,
			Identifier:  fmt.Sprintf("urn:ranobelib:%s:%s", opts.Title.Slug, opts.Volume.Label),
			Language:    "ru",
			ReleaseDate: opts.Title.ReleaseDate,
		},
		Chapters: chapters,
		Cover:    cover,
		Images:   images,
	}

	data, err := epub.Build(book)
	if err != nil {
		return Result{}, err
	}
	return Result{Bytes: data, Warnings: warnings}, nil
}
