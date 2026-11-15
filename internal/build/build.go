// Package build turns a source's chapters into EPUB files, one volume at a time.
package build

import (
	"context"
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

// VolumeTitle is the human-readable name of a volume.
func VolumeTitle(label string) string {
	if label == "" || label == "0" {
		return "Без тома"
	}
	return "Том " + label
}

// Options controls one volume build.
type Options struct {
	Source        source.Source
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
