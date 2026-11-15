// Package reconcile compares the database with the library on disk.
//
// The library is the source of truth: a volume that is no longer there was deleted by the user,
// and a volume that is there was imported. Nothing is deleted from the database, only the status
// changes, so the history of what was built stays intact.
package reconcile

import (
	"context"
	"fmt"

	"github.com/Mimic890/yomibako/internal/library"
	"github.com/Mimic890/yomibako/internal/logx"
	"github.com/Mimic890/yomibako/internal/store"
)

// Report summarises one run.
type Report struct {
	Imported int
	Missing  int
	Unknown  int
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

	// Books in the library that the database does not know about.
	known := make(map[string]map[string]bool, len(titles))
	for _, title := range titles {
		known[title.Slug] = make(map[string]bool, len(title.Volumes))
		for _, volume := range title.Volumes {
			known[title.Slug][volume.Label] = true
		}
	}
	for slug, labels := range present {
		for label := range labels {
			if !known[slug][label] {
				report.Unknown++
			}
		}
	}

	return report, nil
}
