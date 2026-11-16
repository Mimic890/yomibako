// Package check compares subscriptions with the source: which volumes are new, and which already
// built volumes have grown since they were downloaded.
package check

import (
	"context"
	"fmt"

	"github.com/Mimic890/yomibako/internal/build"
	"github.com/Mimic890/yomibako/internal/source"
	"github.com/Mimic890/yomibako/internal/store"
)

// Update describes a volume that exists in the database but has more chapters on the site.
type Update struct {
	Label string
	Have  int
	Site  int
}

// TitleReport is the result for one subscription.
type TitleReport struct {
	Title   store.Title
	New     []build.Volume
	Updated []Update
	Err     error
}

// Resolver returns the source a stored title belongs to.
type Resolver func(title store.Title) (source.Source, bool)

// Run checks every subscription.
func Run(ctx context.Context, st *store.Store, resolve Resolver) ([]TitleReport, error) {
	subscriptions, err := st.Subscriptions(ctx)
	if err != nil {
		return nil, err
	}

	reports := make([]TitleReport, 0, len(subscriptions))
	for _, title := range subscriptions {
		report := TitleReport{Title: title}

		src, ok := resolve(title)
		if !ok {
			report.Err = fmt.Errorf("источник %q не настроен", title.Source)
			reports = append(reports, report)
			continue
		}

		chapters, err := src.Chapters(ctx, title.Slug)
		if err != nil {
			report.Err = err
			reports = append(reports, report)
			continue
		}
		volumes := build.GroupVolumes(chapters)

		recorded, err := st.Volumes(ctx, title.ID)
		if err != nil {
			return nil, err
		}
		byLabel := make(map[string]store.Volume, len(recorded))
		for _, volume := range recorded {
			byLabel[volume.Label] = volume
		}

		for _, volume := range volumes {
			known, ok := byLabel[volume.Label]
			if !ok {
				report.New = append(report.New, volume)
				continue
			}
			// A volume adopted from the library has no chapter count yet; fill it in silently.
			if known.Chapters == 0 && len(volume.Chapters) > 0 {
				_ = st.SetVolumeChapters(ctx, title.ID, volume.Label, len(volume.Chapters))
				continue
			}
			if len(volume.Chapters) > known.Chapters {
				report.Updated = append(report.Updated, Update{
					Label: volume.Label,
					Have:  known.Chapters,
					Site:  len(volume.Chapters),
				})
			}
		}

		last := ""
		if len(volumes) > 0 {
			last = volumes[len(volumes)-1].Label
		}
		_ = st.MarkChecked(ctx, title.ID, last)

		reports = append(reports, report)
	}
	return reports, nil
}
