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
	"github.com/Mimic890/yomibako/internal/source"
	"github.com/Mimic890/yomibako/internal/sources/ranobelib"
)

const usage = `yomibako - загрузка тайтлов в EPUB

Использование:
  yomibako search <запрос> [-n 10] [-p 1]
  yomibako info <slug|ссылка>
  yomibako volumes <slug|ссылка>
  yomibako add <slug|ссылка> [--volume 1,2 | --all] [--out DIR] [--no-images] [--limit N]

Флаги:
  -c, --config PATH   файл конфигурации (по умолчанию yomibako.yml)
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
	case "search":
		return runSearch(rest)
	case "info":
		return runInfo(rest)
	case "volumes":
		return runVolumes(rest)
	case "add":
		return runAdd(rest)
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
	fs.BoolVar(&c.wait, "wait", false, "дождаться, пока BookOrbit заберёт файл")
}

func (c *common) load() (config.Config, *ranobelib.Source, error) {
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

	client := ranobelib.NewClient()
	client.APIBase = cfg.APIBase
	client.SiteBase = cfg.SiteBase
	client.SiteID = cfg.SiteID
	client.Interval = time.Duration(cfg.RequestInterval)
	if cfg.UserAgent != "" {
		client.UserAgent = cfg.UserAgent
	}

	client.Token = cfg.Auth.Token
	if c.token != "" {
		client.Token = c.token
	}
	cookiesFile := cfg.Auth.CookiesFile
	if c.cookies != "" {
		cookiesFile = c.cookies
	}
	if cookiesFile != "" {
		cookies, err := ranobelib.LoadCookies(cookiesFile)
		if err != nil {
			return cfg, nil, err
		}
		client.Cookies = cookies
	}
	return cfg, &ranobelib.Source{Client: client}, nil
}

func contextWithSignals() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// ── search ───────────────────────────────────────────────────────────────────────────────────────

func runSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	var c common
	c.register(fs)
	limit := fs.Int("n", 10, "сколько результатов показать")
	page := fs.Int("p", 1, "страница")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("нужен поисковый запрос")
	}
	query := strings.Join(fs.Args(), " ")

	_, src, err := c.load()
	if err != nil {
		return err
	}
	ctx, cancel := contextWithSignals()
	defer cancel()

	result, err := src.Search(ctx, query, *page)
	if err != nil {
		return err
	}
	if len(result.Items) == 0 {
		fmt.Println("ничего не найдено")
		return nil
	}
	for i, item := range result.Items {
		if i >= *limit {
			break
		}
		meta := make([]string, 0, 3)
		if item.Type != "" {
			meta = append(meta, item.Type)
		}
		if item.Status != "" {
			meta = append(meta, item.Status)
		}
		if item.ReleaseDate != "" {
			meta = append(meta, item.ReleaseDate)
		}
		fmt.Printf("%2d. %s\n    %s\n", i+1, item.Title, item.Slug)
		if len(meta) > 0 {
			fmt.Printf("    %s\n", strings.Join(meta, " · "))
		}
	}
	if result.HasNextPage {
		fmt.Printf("\nесть следующая страница: -p %d\n", result.Page+1)
	}
	return nil
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
		return errors.New("нужен slug или ссылка")
	}
	slug := parseSlug(fs.Arg(0))

	_, src, err := c.load()
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
		return errors.New("нужен slug или ссылка")
	}
	slug := parseSlug(fs.Arg(0))

	_, src, err := c.load()
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
	all := fs.Bool("all", false, "все тома")
	limit := fs.Int("limit", 0, "ограничить число глав в томе")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("нужен slug или ссылка")
	}
	slug := parseSlug(fs.Arg(0))

	cfg, src, err := c.load()
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
	if len(volumes) == 0 {
		return errors.New("у тайтла нет глав")
	}

	selected, err := selectVolumes(volumes, *volumeList, *all)
	if err != nil {
		return err
	}

	covers, err := src.VolumeCovers(ctx, title.Slug)
	if err != nil {
		fmt.Fprintln(os.Stderr, "предупреждение: обложки томов недоступны:", err)
		covers = map[string]string{}
	}

	dir := filepath.Join(cfg.OutputDir, safeName(title.Title))
	if cfg.BookDock != "" {
		dir = cfg.BookDock
	}
	fmt.Printf("%s\n%s\n\n", title.Title, dir)

	for _, volume := range selected {
		if *limit > 0 && len(volume.Chapters) > *limit {
			volume.Chapters = volume.Chapters[:*limit]
		}
		coverURL := covers[volume.Label]
		if coverURL == "" {
			coverURL = title.CoverURL
		}

		started := time.Now()
		result, err := build.BuildVolume(ctx, build.Options{
			Source:        src,
			Title:         title,
			Volume:        volume,
			CoverURL:      coverURL,
			IncludeImages: cfg.ImagesEnabled(),
			Progress: func(format string, args ...any) {
				fmt.Fprintf(os.Stderr, "  "+format+"\n", args...)
			},
		})
		if err != nil {
			return fmt.Errorf("%s: %w", build.VolumeTitle(volume.Label), err)
		}

		var path string
		if cfg.BookDock != "" {
			if err := os.MkdirAll(cfg.BookDock, 0o755); err != nil {
				return err
			}
			path = filepath.Join(cfg.BookDock, dockFileName(title.Title, volume))
		} else {
			volumeDir := filepath.Join(dir, volumeDirName(volume))
			if err := os.MkdirAll(volumeDir, 0o755); err != nil {
				return err
			}
			path = filepath.Join(volumeDir, "book.epub")
		}
		if err := os.WriteFile(path, result.Bytes, 0o644); err != nil {
			return err
		}

		fmt.Printf("%s: %d глав, %s, %s\n", build.VolumeTitle(volume.Label), len(volume.Chapters),
			humanSize(len(result.Bytes)), time.Since(started).Round(time.Second))
		for _, warning := range result.Warnings {
			fmt.Fprintln(os.Stderr, "  предупреждение:", warning)
		}
		fmt.Println("  ->", path)
		if cfg.BookDock != "" && c.wait {
			if err := waitForDock(ctx, path); err != nil {
				return err
			}
		}
	}
	return nil
}

// waitForDock blocks until BookOrbit has taken the file out of the dock, which is what happens
// when it is ingested and finalized.
func waitForDock(ctx context.Context, path string) error {
	deadline := time.Now().Add(10 * time.Minute)
	for {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Println("  BookOrbit забрал файл")
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

// dockFileName is the name a volume gets inside the dock. BookOrbit reads the real metadata from
// the EPUB itself; the name only has to be unique and readable.
func dockFileName(title string, volume build.Volume) string {
	return fmt.Sprintf("%s - %s.epub", safeName(title), volumeDirName(volume))
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
// the first non-flag token, so `search "запрос" -n 3` would otherwise treat "-n 3" as part of the
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

func volumeDirName(volume build.Volume) string {
	if volume.Number > 0 {
		return fmt.Sprintf("Том %02d", volume.Number)
	}
	return "Без тома"
}

func safeName(value string) string {
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
