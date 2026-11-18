// Package pipeline turns selected chapters or volumes into files: it builds each one, writes it
// either to an output directory or straight into the BookOrbit dock, waits for the import when
// asked, and records the result in the database.
package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mimic890/yomibako/internal/build"
	"github.com/Mimic890/yomibako/internal/logx"
	"github.com/Mimic890/yomibako/internal/source"
	"github.com/Mimic890/yomibako/internal/store"
)

// Options describes one run.
type Options struct {
	Source  source.Source
	Store   *store.Store
	Title   source.Title
	TitleID int64
	Volumes []build.Volume // volumes to build, both EPUB and CBZ
	Covers  map[string]string
	// CoverPaths point at local cover files for volumes the source has no cover for.
	CoverPaths map[string]string
	// Layout is how the title is split into files: single, volumes or chapters.
	Layout string
	// Format is the file format to write: epub or cbz.
	Format        string
	Dock          string
	OutDir        string
	IncludeImages bool
	WaitForDock   bool
	Progress      func(format string, args ...any)
	// OnVolume reports a finished file, so a queue can show how far a title has got.
	OnVolume func(done, total int, result Result)
	// OnChapter reports the chapter being built inside the current volume.
	OnChapter func(done, total int)
}

// Result is one written file.
type Result struct {
	Label    string
	Chapters int
	Pages    int
	Path     string
	Size     int
	Imported bool
	Warnings []string
	Duration time.Duration
}

// units turns the selected volumes into the units that become files: the layout decides whether a
// volume, a chapter or the whole title is one file.
func units(opts Options) []build.Volume {
	switch opts.Layout {
	case build.LayoutSingle:
		all := make([]source.Chapter, 0, 256)
		for _, volume := range opts.Volumes {
			all = append(all, volume.Chapters...)
		}
		return []build.Volume{{Label: "single", Display: opts.Title.Title, Chapters: all}}
	case build.LayoutChapters:
		units := make([]build.Volume, 0, 128)
		for _, volume := range opts.Volumes {
			for _, chapter := range volume.Chapters {
				units = append(units, build.Volume{
					Label:    chapter.DisplayTitle,
					Display:  chapter.DisplayTitle,
					Chapters: []source.Chapter{chapter},
				})
			}
		}
		return units
	default:
		return opts.Volumes
	}
}

// unitName is the file name of a unit, without the extension.
func unitName(opts Options, volume build.Volume) string {
	if opts.Layout == build.LayoutSingle {
		return SafeName(opts.Title.Title)
	}
	return DockFileName(opts.Title.Title, build.Display(volume))
}

// Run builds everything selected, one file at a time.
func Run(ctx context.Context, opts Options) ([]Result, error) {
	if opts.Source.Kind() == source.KindManga {
		return runManga(ctx, opts)
	}
	return runNovel(ctx, opts)
}

func runNovel(ctx context.Context, opts Options) ([]Result, error) {
	novel, ok := opts.Source.(source.NovelSource)
	if !ok {
		return nil, fmt.Errorf("источник %s не умеет отдавать текст глав", opts.Source.Name())
	}
	progress := opts.Progress
	if progress == nil {
		progress = func(string, ...any) {}
	}

	planned := units(opts)
	results := make([]Result, 0, len(planned))
	for _, volume := range planned {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		started := time.Now()
		logx.Infof("%s: сборка, глав %d", build.Display(volume), len(volume.Chapters))

		coverURL := opts.Covers[volume.Label]
		if coverURL == "" {
			coverURL = opts.Title.CoverURL
		}
		built, err := build.BuildVolume(ctx, build.Options{
			Source:        novel,
			Title:         opts.Title,
			Volume:        volume,
			CoverURL:      coverURL,
			IncludeImages: opts.IncludeImages,
			Progress:      progress,
			OnChapter:     opts.OnChapter,
		})
		if err != nil {
			return results, fmt.Errorf("%s: %w", build.Display(volume), err)
		}

		path, err := writeFile(opts, unitName(opts, volume)+".epub", VolumeDirName(volume), "book.epub", built.Bytes)
		if err != nil {
			return results, err
		}

		imported := false
		if opts.Dock != "" && !opts.WaitForDock {
			logx.Infof("%s: лежит в book-dock, ждёт подтверждения", build.Display(volume))
		}
		if opts.Dock != "" && opts.WaitForDock {
			if err := WaitForDock(ctx, path); err != nil {
				return results, err
			}
			imported = true
		}

		if opts.Store != nil {
			if err := record(ctx, opts.Store, opts.TitleID, store.Volume{
				Label:    volume.Label,
				Number:   volume.Number,
				Chapters: len(volume.Chapters),
				Format:   "epub",
			}, path, len(built.Bytes), imported); err != nil {
				return results, err
			}
		}

		logx.Infof("%s: готово, %s, %s", build.Display(volume), humanSize(len(built.Bytes)), time.Since(started).Round(time.Second))
		results = append(results, Result{
			Label:    build.Display(volume),
			Chapters: len(volume.Chapters),
			Path:     path,
			Size:     len(built.Bytes),
			Imported: imported,
			Warnings: built.Warnings,
			Duration: time.Since(started),
		})
		if opts.OnVolume != nil {
			opts.OnVolume(len(results), len(planned), results[len(results)-1])
		}
	}
	return results, nil
}

func runManga(ctx context.Context, opts Options) ([]Result, error) {
	manga, ok := opts.Source.(source.MangaSource)
	if !ok {
		return nil, fmt.Errorf("источник %s не умеет отдавать страницы глав", opts.Source.Name())
	}
	progress := opts.Progress
	if progress == nil {
		progress = func(string, ...any) {}
	}

	format := opts.Format
	if format == "" {
		format = build.FormatCBZ
	}
	planned := units(opts)
	results := make([]Result, 0, len(planned))
	for _, volume := range planned {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		started := time.Now()
		label := build.Display(volume)
		logx.Infof("%s: сборка, глав %d", label, len(volume.Chapters))

		dirName := VolumeDirName(volume)
		if opts.Layout == build.LayoutSingle {
			// One file for the whole title: the folder repeats the title otherwise.
			dirName = "single"
		}
		path, err := targetPath(opts, unitName(opts, volume)+"."+format, dirName, "book."+format)
		if err != nil {
			return results, err
		}
		built, err := build.BuildComicVolume(ctx, build.VolumeOptions{
			Source:    manga,
			Title:     opts.Title,
			Volume:    volume,
			CoverURL:  opts.Covers[volume.Label],
			Path:      path,
			Progress:  progress,
			Format:    format,
			OnChapter: opts.OnChapter,
		})
		if err != nil {
			return results, fmt.Errorf("%s: %w", label, err)
		}

		imported := false
		if opts.Dock != "" && !opts.WaitForDock {
			logx.Infof("%s: лежит в book-dock, ждёт подтверждения", label)
		}
		if opts.Dock != "" && opts.WaitForDock {
			if err := WaitForDock(ctx, path); err != nil {
				return results, err
			}
			imported = true
		}

		if opts.Store != nil {
			if err := record(ctx, opts.Store, opts.TitleID, store.Volume{
				Label:    volume.Label,
				Number:   volume.Number,
				Chapters: len(volume.Chapters),
				Format:   format,
			}, path, int(built.Size), imported); err != nil {
				return results, err
			}
		}

		logx.Infof("%s: готово, глав %d, страниц %d, %s", label, built.Chapters, built.Pages, humanSize(int(built.Size)))
		results = append(results, Result{
			Label:    label,
			Chapters: built.Chapters,
			Pages:    built.Pages,
			Path:     path,
			Size:     int(built.Size),
			Imported: imported,
			Duration: time.Since(started),
		})
		if opts.OnVolume != nil {
			opts.OnVolume(len(results), len(planned), results[len(results)-1])
		}
	}
	return results, nil
}

// targetPath decides where a file goes and creates the folder.
func targetPath(opts Options, dockName, dirName, fileName string) (string, error) {
	if opts.Dock != "" {
		if err := os.MkdirAll(opts.Dock, 0o755); err != nil {
			return "", err
		}
		return filepath.Join(opts.Dock, dockName), nil
	}
	dir := filepath.Join(opts.OutDir, SafeName(opts.Title.Title), dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

func writeFile(opts Options, dockName, dirName, fileName string, data []byte) (string, error) {
	path, err := targetPath(opts, dockName, dirName, fileName)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func record(ctx context.Context, st *store.Store, titleID int64, volume store.Volume, path string, size int, imported bool) error {
	status := store.StatusBuilt
	var importedAt *time.Time
	if imported {
		status = store.StatusImported
		nowValue := time.Now()
		importedAt = &nowValue
	}
	volume.TitleID = titleID
	volume.Status = status
	volume.FilePath = path
	volume.SizeBytes = int64(size)
	volume.BuiltAt = time.Now()
	volume.ImportedAt = importedAt
	return st.RecordVolume(ctx, volume)
}

// WaitForDock blocks until BookOrbit has taken the file out of the dock, which is what happens
// when it is ingested and finalized.
func WaitForDock(ctx context.Context, path string) error {
	deadline := time.Now().Add(10 * time.Minute)
	for {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("файл всё ещё в book-dock через 10 минут: %s", path)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// DockFileName is the name a file gets inside the dock, without the extension. BookOrbit reads the
// real metadata from the file itself; the name only has to be unique and readable.
func DockFileName(title, label string) string {
	return fmt.Sprintf("%s - %s", SafeName(title), label)
}

// ChapterLabel is the human-readable name of a manga chapter.
func ChapterLabel(chapter source.Chapter) string {
	if chapter.Number != "" {
		return "Глава " + chapter.Number
	}
	return fmt.Sprintf("Глава %g", chapter.Index)
}

// VolumeDirName is the folder name of a volume in the output directory.
func VolumeDirName(volume build.Volume) string {
	if volume.Display != "" {
		return SafeName(volume.Display)
	}
	if volume.Number > 0 {
		return fmt.Sprintf("Том %02d", volume.Number)
	}
	return "Без тома"
}

// SafeName makes a string usable as a file or folder name.
func SafeName(value string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "-", `"`, "-", "<", "-", ">", "-", "|", "-")
	name := strings.TrimSpace(replacer.Replace(value))
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "book"
	}
	if len([]rune(name)) > 120 {
		name = string([]rune(name)[:120])
	}
	return strings.TrimRight(name, ". ")
}

func humanSize(bytes int) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d Б", bytes)
	}
	div, exp := int64(unit), 0
	for n := int64(bytes) / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), []string{"КБ", "МБ", "ГБ"}[exp])
}
