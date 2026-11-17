// Package check compares a title with its source: which volumes are new, and which already built
// volumes have grown since they were downloaded.
package check

import (
	"context"
	"fmt"
	"strings"

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

// TitleReport is the result for one title.
type TitleReport struct {
	Title   store.Title
	New     []build.Volume
	Updated []Update
	Skipped bool
	Err     error
}

// Resolver returns the source a stored title belongs to.
type Resolver func(title store.Title) (source.Source, bool)

// Options tunes a bulk run.
type Options struct {
	// SkipCompleted leaves finished titles alone: their chapters do not appear any more, and a
	// manual link still checks them.
	SkipCompleted bool
}

// Run checks every subscription. Subscriptions are the titles the user asked to keep an eye on;
// there is no timer anywhere, this only runs when the command is typed.
func Run(ctx context.Context, st *store.Store, resolve Resolver, opts Options) ([]TitleReport, error) {
	subscriptions, err := st.Subscriptions(ctx)
	if err != nil {
		return nil, err
	}

	reports := make([]TitleReport, 0, len(subscriptions))
	for _, title := range subscriptions {
		if opts.SkipCompleted && Completed(title.Status) {
			reports = append(reports, TitleReport{Title: title, Skipped: true})
			continue
		}
		reports = append(reports, Check(ctx, st, resolve, title))
	}
	return reports, nil
}

// Check inspects one title, whatever its status.
func Check(ctx context.Context, st *store.Store, resolve Resolver, title store.Title) TitleReport {
	report := TitleReport{Title: title}

	src, ok := resolve(title)
	if !ok {
		report.Err = fmt.Errorf("источник %q не настроен", title.Source)
		return report
	}

	chapters, err := src.Chapters(ctx, title.Slug)
	if err != nil {
		report.Err = err
		return report
	}
	volumes, err := build.PlanVolumes(src.Kind(), chapters)
	if err != nil {
		report.Err = err
		return report
	}

	recorded, err := st.Volumes(ctx, title.ID)
	if err != nil {
		report.Err = err
		return report
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

	// The source's own state is refreshed on every check, so a title that ended is recognised
	// without a manual switch.
	if info, err := src.Title(ctx, title.Slug); err == nil && info.Status != "" {
		_ = st.SetTitleStatus(ctx, title.ID, info.Status)
		report.Title.Status = info.Status
	}
	return report
}

// Completed tells whether a source reports the title as finished.
func Completed(status string) bool {
	value := strings.ToLower(strings.TrimSpace(status))
	if value == "" {
		return false
	}
	for _, marker := range []string{"заверш", "completed", "complete", "finished", "выпуск окончен"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
