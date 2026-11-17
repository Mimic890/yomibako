// Command yomibako downloads light novels from a source and writes them as EPUB volumes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Mimic890/yomibako/internal/build"
	"github.com/Mimic890/yomibako/internal/config"
	"github.com/Mimic890/yomibako/internal/cookies"
	"github.com/Mimic890/yomibako/internal/logx"
	"github.com/Mimic890/yomibako/internal/pipeline"
	"github.com/Mimic890/yomibako/internal/source"
	"github.com/Mimic890/yomibako/internal/sources"
	"github.com/Mimic890/yomibako/internal/sources/ranobelib"
	"github.com/Mimic890/yomibako/internal/store"
)

const usage = `yomibako - загрузка тайтлов в EPUB и CBZ

Использование:
  yomibako info <ссылка>
  yomibako volumes <ссылка>
  yomibako add <ссылка> [--volume 1,2 | --all] [--out DIR] [--dock DIR] [--wait]
  yomibako list
  yomibako subscribe <ссылка> [--interval 24]
  yomibako unsubscribe <ссылка>
  yomibako check
  yomibako sync
  yomibako bot

Флаги:
  -c, --config PATH   файл конфигурации (по умолчанию yomibako.yml)
      --cookies PATH  cookies.txt из браузера (куки всех источников в одном файле)
  -o, --out DIR       куда складывать EPUB (по умолчанию из конфига)
      --no-images     не встраивать иллюстрации
      --limit N       ограничить число глав в томе (для проверки)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}

	command := args[0]
	rest := args[1:]

	switch command {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	case "info":
		return runInfo(rest)
	case "volumes":
		return runVolumes(rest)
	case "add":
		return runAdd(rest)
	case "list":
		return runList(rest)
	case "subscribe":
		return runSubscribe(rest)
	case "unsubscribe":
		return runUnsubscribe(rest)
	case "check":
		return runCheck(rest)
	case "sync":
		return runSync(rest)
	case "bot":
		return runBot(rest)
	default:
		return fmt.Errorf("неизвестная команда %q (см. yomibako help)", command)
	}
}

// ── Shared plumbing ──────────────────────────────────────────────────────────────────────────────

type common struct {
	configPath string
	outDir     string
	noImages   bool
	token      string
	cookies    string
	dock       string
	wait       bool
}

func (c *common) register(fs *flag.FlagSet) {
	fs.StringVar(&c.configPath, "config", "", "файл конфигурации")
	fs.StringVar(&c.configPath, "c", "", "файл конфигурации")
	fs.StringVar(&c.outDir, "out", "", "куда складывать EPUB")
	fs.StringVar(&c.outDir, "o", "", "куда складывать EPUB")
	fs.BoolVar(&c.noImages, "no-images", false, "не встраивать иллюстрации")
	fs.StringVar(&c.token, "token", "", "access token RanobeLib")
	fs.StringVar(&c.cookies, "cookies", "", "файл cookies.txt из браузера")
	fs.StringVar(&c.dock, "dock", "", "папка book-dock BookOrbit")
	fs.BoolVar(&c.wait, "wait", false, "дождаться автофинализации (если она включена в BookOrbit)")
}

func (c *common) load() (config.Config, *sources.Registry, error) {
	cfg, err := config.Load(c.configPath)
	if err != nil {
		return cfg, nil, err
	}
	if c.outDir != "" {
		cfg.OutputDir = c.outDir
	}
	if c.noImages {
		disabled := false
		cfg.IncludeImages = &disabled
	}
	if c.dock != "" {
		cfg.BookDock = c.dock
	}
	if err := logx.SetLevel(cfg.LogLevel); err != nil {
		return cfg, nil, err
	}

	if c.token != "" {
		cfg.Auth.Token = c.token
	}
	// A token saved from the bot wins over the config file.
	if st, err := store.Open(cfg.Database); err == nil {
		if saved, ok, err := st.Setting(context.Background(), "ranobelib_token"); err == nil && ok {
			cfg.Auth.Token = saved
		}
		st.Close()
	}

	cookiesFile := cfg.Auth.CookiesFile
	if c.cookies != "" {
		cookiesFile = c.cookies
	}

	registry := sources.New(cfg)
	if cookiesFile != "" {
		jar, err := cookies.Load(cookiesFile)
		if err != nil {
			return cfg, nil, err
		}
		logx.Infof("куки прочитаны для: %s", strings.Join(jar.Domains(), ", "))
		registry.SetCookies(jar)
	}
	return cfg, registry, nil
}

func contextWithSignals() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// ── info ────────────────────────────────────────────────────────────────────────────────────────

func runInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	var c common
	c.register(fs)
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("нужна полная ссылка на тайтл")
	}
	_, registry, err := c.load()
	if err != nil {
		return err
	}
	src, slug, err := resolveLink(registry, fs.Arg(0))
	if err != nil {
		return err
	}
	ctx, cancel := contextWithSignals()
	defer cancel()

	title, err := src.Title(ctx, slug)
	if err != nil {
		return err
	}
	fmt.Println(title.Title)
	if len(title.AlternativeTitles) > 0 {
		fmt.Println("  также:", strings.Join(title.AlternativeTitles, ", "))
	}
	if len(title.Authors) > 0 {
		fmt.Println("  автор:", strings.Join(title.Authors, ", "))
	}
	if title.Status != "" || title.Type != "" {
		fmt.Printf("  %s · %s\n", title.Type, title.Status)
	}
	if len(title.Genres) > 0 {
		fmt.Println("  жанры:", strings.Join(title.Genres, ", "))
	}
	if title.Description != "" {
		fmt.Println()
		fmt.Println(title.Description)
	}
	fmt.Println()
	fmt.Println("  источник:", title.SourceURL)
	return nil
}

// ── volumes ─────────────────────────────────────────────────────────────────────────────────────

func runVolumes(args []string) error {
	fs := flag.NewFlagSet("volumes", flag.ContinueOnError)
	var c common
	c.register(fs)
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("нужна полная ссылка на тайтл")
	}
	_, registry, err := c.load()
	if err != nil {
		return err
	}
	src, slug, err := resolveLink(registry, fs.Arg(0))
	if err != nil {
		return err
	}
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
	fmt.Printf("%s\nглав: %d, томов: %d\n\n", title.Title, len(chapters), len(volumes))
	for _, volume := range volumes {
		fmt.Printf("%-10s %3d глав  (%s ... %s)\n",
			build.VolumeTitle(volume.Label), len(volume.Chapters),
			volume.Chapters[0].DisplayTitle, volume.Chapters[len(volume.Chapters)-1].DisplayTitle)
	}
	return nil
}

// ── add ─────────────────────────────────────────────────────────────────────────────────────────

func runAdd(args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	var c common
	c.register(fs)
	volumeList := fs.String("volume", "", "номера томов через запятую")
	all := fs.Bool("all", false, "все тома или главы")
	limit := fs.Int("limit", 0, "ограничить число глав в томе")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("нужна полная ссылка на тайтл")
	}
	cfg, registry, err := c.load()
	if err != nil {
		return err
	}
	src, slug, err := resolveLink(registry, fs.Arg(0))
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
	if len(volumes) == 0 {
		return errors.New("у тайтла нет глав")
	}

	titleID, err := recordTitle(ctx, st, src, title, len(chapters), len(volumes))
	if err != nil {
		return err
	}

	volumes, err = build.PlanVolumes(src.Kind(), chapters)
	if err != nil {
		return err
	}

	selected, err := selectVolumes(volumes, *volumeList, *all)
	if err != nil {
		return err
	}
	if err != nil {
		return err
	}

	covers := map[string]string{}
	if coversSource, ok := src.(source.CoverSource); ok {
		if found, err := coversSource.VolumeCovers(ctx, title.Slug); err == nil {
			covers = found
		} else {
			fmt.Fprintln(os.Stderr, "предупреждение: обложки томов недоступны:", err)
		}
	}

	dir := filepath.Join(cfg.OutputDir, pipeline.SafeName(title.Title))
	if cfg.BookDock != "" {
		dir = cfg.BookDock
	}
	fmt.Printf("%s\n%s\n\n", title.Title, dir)

	for i := range selected {
		if *limit > 0 && len(selected[i].Chapters) > *limit {
			selected[i].Chapters = selected[i].Chapters[:*limit]
		}
	}

	results, err := pipeline.Run(ctx, pipeline.Options{
		Source:        src,
		Store:         st,
		Title:         title,
		TitleID:       titleID,
		Volumes:       selected,
		Covers:        covers,
		Dock:          cfg.BookDock,
		OutDir:        cfg.OutputDir,
		IncludeImages: cfg.ImagesEnabled(),
		WaitForDock:   c.wait,
		Progress: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "  "+format+"\n", args...)
		},
	})
	for _, result := range results {
		detail := fmt.Sprintf("%d глав", result.Chapters)
		if result.Pages > 0 {
			detail = fmt.Sprintf("%d страниц", result.Pages)
		}
		fmt.Printf("%s: %s, %s, %s\n", result.Label, detail,
			humanSize(result.Size), result.Duration.Round(time.Second))
		for _, warning := range result.Warnings {
			fmt.Fprintln(os.Stderr, "  предупреждение:", warning)
		}
		fmt.Println("  ->", result.Path)
		if result.Imported {
			fmt.Println("  BookOrbit забрал файл")
		}
	}
	if err != nil {
		return err
	}
	return nil
}

func selectVolumes(volumes []build.Volume, list string, all bool) ([]build.Volume, error) {
	if all || strings.TrimSpace(list) == "" {
		return volumes, nil
	}
	wanted := make(map[string]struct{})
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		wanted[part] = struct{}{}
	}
	var out []build.Volume
	for _, volume := range volumes {
		if _, ok := wanted[volume.Label]; ok {
			out = append(out, volume)
		}
	}
	if len(out) == 0 {
		available := make([]string, 0, len(volumes))
		for _, volume := range volumes {
			available = append(available, volume.Label)
		}
		return nil, fmt.Errorf("тома %q нет, доступны: %s", list, strings.Join(available, ", "))
	}
	return out, nil
}

// reorderArgs moves flags in front of positional arguments. The standard flag package stops at
// the first non-flag token, so `add "<ссылка>" --all` would otherwise treat "--all" as part of the
// query.
func reorderArgs(args []string) []string {
	boolean := map[string]bool{"--no-images": true, "--all": true, "-h": true, "--help": true}
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") && !boolean[arg] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, arg)
	}
	return append(flags, positional...)
}

// ── Helpers ─────────────────────────────────────────────────────────────────────────────────────

// parseSlug accepts a bare slug or any RanobeLib URL that contains one.
func parseSlug(input string) string {
	input = strings.TrimSpace(input)
	if !strings.Contains(input, "/") {
		return input
	}
	if parsed, err := url.Parse(input); err == nil && parsed.Path != "" {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		for i, part := range parts {
			if part == "book" && i+1 < len(parts) {
				return parts[i+1]
			}
		}
		return parts[len(parts)-1]
	}
	parts := strings.Split(strings.Trim(input, "/"), "/")
	return parts[len(parts)-1]
}

func humanSize(bytes int) string {
	const unit = 1024
	if bytes < unit {
		return strconv.Itoa(bytes) + " Б"
	}
	div, exp := int64(unit), 0
	for n := int64(bytes) / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), []string{"КБ", "МБ", "ГБ"}[exp])
}

var _ source.Source = (*ranobelib.Source)(nil)

// resolveLink turns a link into its source, with a hint when the user typed a slug instead.
func resolveLink(registry *sources.Registry, input string) (source.Source, string, error) {
	src, slug, err := registry.Resolve(input)
	if errors.Is(err, sources.ErrNotURL) {
		return nil, "", errors.New("нужна полная ссылка на тайтл, например https://ranobelib.me/ru/book/20818--lord-of-the-mysteries")
	}
	return src, slug, err
}
