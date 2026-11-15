package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Mimic890/yomibako/internal/build"
	"github.com/Mimic890/yomibako/internal/config"
	"github.com/Mimic890/yomibako/internal/source"
	"github.com/Mimic890/yomibako/internal/store"
)

// openStore opens the database named in the config.
func openStore(cfg config.Config) (*store.Store, error) {
	return store.Open(cfg.Database)
}

// recordTitle stores or refreshes a title and returns its id.
func recordTitle(ctx context.Context, st *store.Store, src source.Source, title source.Title, chapters int, volumes int) (int64, error) {
	return st.UpsertTitle(ctx, store.Title{
		Source:        src.Name(),
		Slug:          title.Slug,
		Title:         title.Title,
		Authors:       title.Authors,
		CoverURL:      title.CoverURL,
		SourceURL:     title.SourceURL,
		TotalChapters: chapters,
		TotalVolumes:  volumes,
	})
}

// ── list ────────────────────────────────────────────────────────────────────────────────────────

func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	var c common
	c.register(fs)
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}

	cfg, _, err := c.load()
	if err != nil {
		return err
	}
	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, cancel := contextWithSignals()
	defer cancel()

	titles, err := st.Titles(ctx)
	if err != nil {
		return err
	}
	if len(titles) == 0 {
		fmt.Println("в базе пока пусто")
		return nil
	}

	for _, title := range titles {
		mark := " "
		if title.Subscription != nil && title.Subscription.Enabled {
			mark = "*"
		}
		fmt.Printf("%s %s\n", mark, title.Title)
		fmt.Printf("    %s · %s\n", title.Slug, title.Source)
		if len(title.Volumes) == 0 {
			fmt.Println("    томов нет")
			continue
		}
		for _, volume := range title.Volumes {
			status := volume.Status
			if volume.Status == store.StatusImported {
				status = "импортирован"
			} else if volume.Status == store.StatusBuilt {
				status = "собран"
			}
			fmt.Printf("    %-10s %3d глав  %-12s %s\n",
				build.VolumeTitle(volume.Label), volume.Chapters, status, humanSize(int(volume.SizeBytes)))
		}
	}
	fmt.Println()
	fmt.Println("* - подписка включена")
	return nil
}

// ── subscribe / unsubscribe ────────────────────────────────────────────────────────────────────

func runSubscribe(args []string) error {
	fs := flag.NewFlagSet("subscribe", flag.ContinueOnError)
	var c common
	c.register(fs)
	interval := fs.Int("interval", 24, "как часто проверять, в часах")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("нужен slug или ссылка")
	}
	return changeSubscription(c, fs.Arg(0), *interval, true)
}

func runUnsubscribe(args []string) error {
	fs := flag.NewFlagSet("unsubscribe", flag.ContinueOnError)
	var c common
	c.register(fs)
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("нужен slug или ссылка")
	}
	return changeSubscription(c, fs.Arg(0), 0, false)
}

func changeSubscription(c common, rawSlug string, interval int, subscribe bool) error {
	cfg, src, err := c.load()
	if err != nil {
		return err
	}
	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, cancel := contextWithSignals()
	defer cancel()

	title, err := src.Title(ctx, parseSlug(rawSlug))
	if err != nil {
		return err
	}
	chapters, err := src.Chapters(ctx, title.Slug)
	if err != nil {
		return err
	}
	volumes := build.GroupVolumes(chapters)
	id, err := recordTitle(ctx, st, src, title, len(chapters), len(volumes))
	if err != nil {
		return err
	}

	if subscribe {
		if err := st.Subscribe(ctx, id, interval); err != nil {
			return err
		}
		fmt.Printf("подписка на %s, проверка каждые %d ч\n", title.Title, interval)
		return nil
	}
	if err := st.Unsubscribe(ctx, id); err != nil {
		return err
	}
	fmt.Printf("подписка на %s снята\n", title.Title)
	return nil
}

// ── check ──────────────────────────────────────────────────────────────────────────────────────

func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	var c common
	c.register(fs)
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}

	cfg, src, err := c.load()
	if err != nil {
		return err
	}
	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, cancel := contextWithSignals()
	defer cancel()

	subscriptions, err := st.Subscriptions(ctx)
	if err != nil {
		return err
	}
	if len(subscriptions) == 0 {
		fmt.Println("подписок нет")
		return nil
	}

	found := 0
	for _, title := range subscriptions {
		chapters, err := src.Chapters(ctx, title.Slug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", title.Title, err)
			continue
		}
		volumes := build.GroupVolumes(chapters)
		known, err := st.VolumeLabels(ctx, title.ID)
		if err != nil {
			return err
		}

		var fresh []build.Volume
		for _, volume := range volumes {
			if _, ok := known[volume.Label]; !ok {
				fresh = append(fresh, volume)
			}
		}

		last := ""
		if len(volumes) > 0 {
			last = volumes[len(volumes)-1].Label
		}
		if err := st.MarkChecked(ctx, title.ID, last); err != nil {
			return err
		}

		if len(fresh) == 0 {
			fmt.Printf("%s: новых томов нет\n", title.Title)
			continue
		}
		found += len(fresh)
		fmt.Printf("%s: новых томов %d\n", title.Title, len(fresh))
		for _, volume := range fresh {
			fmt.Printf("    %s (%d глав)\n", build.VolumeTitle(volume.Label), len(volume.Chapters))
		}
	}

	if found == 0 {
		return nil
	}
	fmt.Printf("\nвсего новых томов: %d\n", found)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────────────────────

// recordVolume stores a built volume and, when BookOrbit already took the file, marks it imported.
func recordVolume(ctx context.Context, st *store.Store, titleID int64, volume build.Volume, path string, size int, imported bool) error {
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
