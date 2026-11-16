package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/Mimic890/yomibako/internal/build"
	"github.com/Mimic890/yomibako/internal/check"
	"github.com/Mimic890/yomibako/internal/config"
	"github.com/Mimic890/yomibako/internal/reconcile"
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
			switch volume.Status {
			case store.StatusImported:
				status = "импортирован"
			case store.StatusBuilt:
				status = "собран"
			case store.StatusMissing:
				status = "нет в библиотеке"
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
	cfg, registry, err := c.load()
	if err != nil {
		return err
	}
	src, slug, err := registry.Resolve(rawSlug)
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

	title, err := src.Title(ctx, slug)
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

	cfg, registry, err := c.load()
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

	reports, err := check.Run(ctx, st, func(title store.Title) (source.Source, bool) {
		return registry.ByName(title.Source)
	})
	if err != nil {
		return err
	}
	if len(reports) == 0 {
		fmt.Println("подписок нет")
		return nil
	}

	newVolumes, updated := 0, 0
	for _, report := range reports {
		if report.Err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", report.Title.Title, report.Err)
			continue
		}
		if len(report.New) == 0 && len(report.Updated) == 0 {
			fmt.Printf("%s: без изменений\n", report.Title.Title)
			continue
		}
		fmt.Printf("%s\n", report.Title.Title)
		for _, volume := range report.New {
			newVolumes++
			fmt.Printf("    новый том %s (%d глав)\n", volume.Label, len(volume.Chapters))
		}
		for _, update := range report.Updated {
			updated++
			fmt.Printf("    том %s: %d глав из %d\n", update.Label, update.Have, update.Site)
		}
	}
	fmt.Printf("\nновых томов: %d, томов с новыми главами: %d\n", newVolumes, updated)
	return nil
}

// ── sync ──────────────────────────────────────────────────────────────────────────────────────

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
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

	report, err := reconcile.Run(ctx, st, cfg.LibraryDir)
	if err != nil {
		return err
	}
	fmt.Printf("импортировано: %d\nнет на диске: %d\nдобавлено из библиотеки: %d\n", report.Imported, report.Missing, report.Adopted)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────────────────────
