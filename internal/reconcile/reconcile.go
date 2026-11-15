// Package reconcile compares the database with the library on disk.
//
// The library is the source of truth: a volume that is no longer there was deleted by the user,
// and a volume that is there was imported. Nothing is deleted from the database, only the status
// changes, so the history of what was built stays intact.
package reconcile

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Mimic890/yomibako/internal/library"
	"github.com/Mimic890/yomibako/internal/logx"
	"github.com/Mimic890/yomibako/internal/store"
)

// Report summarises one run.
type Report struct {
	Imported int
	Missing  int
	Adopted  int
}

func timePtr() *time.Time {
	now := time.Now()
	return &now
}

// adopt finds or creates the title a library book belongs to.
func adopt(ctx context.Context, st *store.Store, entry library.Entry) (int64, error) {
	existing, err := st.FindTitle(ctx, "ranobelib", entry.Slug)
	if err != nil {
		return 0, err
	}
	if existing != nil {
		return existing.ID, nil
	}
	name := entry.Title
	if name == "" {
		name = entry.Slug
	}
	return st.UpsertTitle(ctx, store.Title{Source: "ranobelib", Slug: entry.Slug, Title: name})
}

// Run scans dir and updates the status of every recorded volume.
func Run(ctx context.Context, st *store.Store, dir string) (Report, error) {
	var report Report
	if dir == "" {
		return report, fmt.Errorf("library_dir не задан в конфиге")
	}

	entries, err := library.Scan(ctx, dir)
	if err != nil {
		return report, err
	}

	// slug -> set of volume labels present on disk
	present := make(map[string]map[string]bool)
	for _, entry := range entries {
		if present[entry.Slug] == nil {
			present[entry.Slug] = make(map[string]bool)
		}
		present[entry.Slug][entry.Volume] = true
	}

	titles, err := st.Titles(ctx)
	if err != nil {
		return report, err
	}

	for _, title := range titles {
		onDisk := present[title.Slug]
		for _, volume := range title.Volumes {
			switch {
			case onDisk[volume.Label]:
				if volume.Status != store.StatusImported {
					if err := st.SetVolumeStatus(ctx, title.ID, volume.Label, store.StatusImported); err != nil {
						return report, err
					}
					report.Imported++
					logx.Infof("sync: %s %s — импортирован", title.Title, volume.Label)
				}
			default:
				if volume.Status != store.StatusMissing {
					if err := st.SetVolumeStatus(ctx, title.ID, volume.Label, store.StatusMissing); err != nil {
						return report, err
					}
					report.Missing++
					logx.Infof("sync: %s %s — нет в библиотеке", title.Title, volume.Label)
				}
			}
		}
	}

	// Books in the library that the database does not know about are adopted: the library is the
	// source of truth, so a book that is there was imported at some point.
	known := make(map[string]map[string]bool, len(titles))
	for _, title := range titles {
		known[title.Slug] = make(map[string]bool, len(title.Volumes))
		for _, volume := range title.Volumes {
			known[title.Slug][volume.Label] = true
		}
	}
	for _, entry := range entries {
		if known[entry.Slug][entry.Volume] {
			continue
		}
		titleID, err := adopt(ctx, st, entry)
		if err != nil {
			return report, err
		}
		number, _ := strconv.Atoi(entry.Volume)
		if err := st.RecordVolume(ctx, store.Volume{
			TitleID:    titleID,
			Label:      entry.Volume,
			Number:     number,
			Status:     store.StatusImported,
			FilePath:   entry.Path,
			ImportedAt: timePtr(),
		}); err != nil {
			return report, err
		}
		report.Adopted++
		logx.Infof("sync: %s %s — добавлен из библиотеки", entry.Title, entry.Volume)
	}

	return report, nil
}
