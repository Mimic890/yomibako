// Package pipeline turns selected volumes into files: it builds each EPUB, writes it either to an
// output directory or straight into the BookOrbit dock, waits for the import when asked, and
// records the result in the database.
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
	Source        source.Source
	Store         *store.Store
	Title         source.Title
	TitleID       int64
	Volumes       []build.Volume
	Covers        map[string]string
	Dock          string
	OutDir        string
	IncludeImages bool
	WaitForDock   bool
	Progress      func(format string, args ...any)
}

// Result is one written volume.
type Result struct {
	Volume   build.Volume
	Path     string
	Size     int
	Imported bool
	Warnings []string
	Duration time.Duration
}

// Run builds and writes every volume in order.
func Run(ctx context.Context, opts Options) ([]Result, error) {
	progress := opts.Progress
	if progress == nil {
		progress = func(string, ...any) {}
	}

	results := make([]Result, 0, len(opts.Volumes))
	for _, volume := range opts.Volumes {
		started := time.Now()
		coverURL := opts.Covers[volume.Label]
		if coverURL == "" {
			coverURL = opts.Title.CoverURL
		}
		built, err := build.BuildVolume(ctx, build.Options{
			Source:        opts.Source,
			Title:         opts.Title,
			Volume:        volume,
			CoverURL:      coverURL,
			IncludeImages: opts.IncludeImages,
			Progress:      progress,
		})
		if err != nil {
			return results, fmt.Errorf("%s: %w", build.VolumeTitle(volume.Label), err)
		}

		path, err := write(opts, volume, built.Bytes)
		if err != nil {
			return results, err
		}

		imported := false
		if opts.Dock != "" && !opts.WaitForDock {
			logx.Infof("том %s: лежит в book-dock, ждёт подтверждения", build.VolumeTitle(volume.Label))
		}
		if opts.Dock != "" && opts.WaitForDock {
			if err := WaitForDock(ctx, path); err != nil {
				return results, err
			}
			imported = true
		}

		if opts.Store != nil {
			if err := record(ctx, opts.Store, opts.TitleID, volume, path, len(built.Bytes), imported); err != nil {
				return results, err
			}
		}

		logx.Infof("том %s: готово, %s, %s", build.VolumeTitle(volume.Label), humanSize(len(built.Bytes)), time.Since(started).Round(time.Second))
		results = append(results, Result{
			Volume:   volume,
			Path:     path,
			Size:     len(built.Bytes),
			Imported: imported,
			Warnings: built.Warnings,
			Duration: time.Since(started),
		})
	}
	return results, nil
}

func write(opts Options, volume build.Volume, data []byte) (string, error) {
	var path string
	if opts.Dock != "" {
		if err := os.MkdirAll(opts.Dock, 0o755); err != nil {
			return "", err
		}
		path = filepath.Join(opts.Dock, DockFileName(opts.Title.Title, volume))
	} else {
		dir := filepath.Join(opts.OutDir, SafeName(opts.Title.Title), VolumeDirName(volume))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		path = filepath.Join(dir, "book.epub")
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func record(ctx context.Context, st *store.Store, titleID int64, volume build.Volume, path string, size int, imported bool) error {
	status := store.StatusBuilt
	var importedAt *time.Time
	if imported {
		status = store.StatusImported
		nowValue := time.Now()
		importedAt = &nowValue
	}
	return st.RecordVolume(ctx, store.Volume{
		TitleID:    titleID,
		Label:      volume.Label,
		Number:     volume.Number,
		Chapters:   len(volume.Chapters),
		Status:     status,
		FilePath:   path,
		SizeBytes:  int64(size),
		BuiltAt:    time.Now(),
		ImportedAt: importedAt,
	})
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

// DockFileName is the name a volume gets inside the dock. BookOrbit reads the real metadata from
// the EPUB itself; the name only has to be unique and readable.
func DockFileName(title string, volume build.Volume) string {
	return fmt.Sprintf("%s - %s.epub", SafeName(title), VolumeDirName(volume))
}

// VolumeDirName is the folder name of a volume in the output directory.
func VolumeDirName(volume build.Volume) string {
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
