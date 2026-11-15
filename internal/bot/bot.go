// Package bot is the Telegram front end: it lists what is tracked, starts downloads, and reports
// new volumes. Downloads run one at a time in a worker so the source is never hammered.
package bot

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/Mimic890/yomibako/internal/build"
	"github.com/Mimic890/yomibako/internal/config"
	"github.com/Mimic890/yomibako/internal/logx"
	"github.com/Mimic890/yomibako/internal/pipeline"
	"github.com/Mimic890/yomibako/internal/source"
	"github.com/Mimic890/yomibako/internal/store"
	"github.com/Mimic890/yomibako/internal/telegram"
)

// Bot serves one chat.
type Bot struct {
	api     *telegram.Client
	cfg     config.Config
	src     source.Source
	st      *store.Store
	allowed int64
	jobs    chan job
}

type job struct {
	chatID    int64
	messageID int64
	slug      string
	volumes   string
	all       bool
}

// New builds a bot from the configuration.
func New(cfg config.Config, src source.Source, st *store.Store) *Bot {
	return &Bot{
		api:     telegram.New(cfg.Telegram.Token),
		cfg:     cfg,
		src:     src,
		st:      st,
		allowed: cfg.Telegram.ChatID,
		jobs:    make(chan job, 8),
	}
}

const helpText = `<b>Yomibako</b>

/list - что уже в базе
/add &lt;ссылка|slug&gt; [том] - собрать и отдать в BookOrbit
/check - есть ли новые тома
/subscribe &lt;ссылка|slug&gt; [часы] - следить за тайтлом
/unsubscribe &lt;ссылка|slug&gt; - перестать следить
/status - сводка

Том можно указать как <code>1</code>, <code>1,2</code> или <code>all</code>. Без тома собирается первый.`

// Run polls for updates until the context is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	if b.cfg.Telegram.Token == "" {
		return fmt.Errorf("telegram.token не задан в конфиге")
	}
	if b.allowed == 0 {
		return fmt.Errorf("telegram.chat_id не задан в конфиге")
	}

	me, err := b.api.Me(ctx)
	if err != nil {
		return fmt.Errorf("telegram: токен не принят: %w", err)
	}
	logx.Infof("бот @%s запущен, чат %d", me.Username, b.allowed)

	_ = b.api.SetMyCommands(ctx, [][2]string{
		{"list", "что уже в базе"},
		{"add", "собрать том и отдать в BookOrbit"},
		{"check", "есть ли новые тома"},
		{"subscribe", "следить за тайтлом"},
		{"unsubscribe", "перестать следить"},
		{"status", "сводка"},
		{"help", "справка"},
	})

	go b.worker(ctx)

	var offset int64
	for {
		updates, err := b.api.GetUpdates(ctx, offset, 50*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logx.Errorf("telegram: %v", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(3 * time.Second):
			}
			continue
		}
		for _, update := range updates {
			offset = update.UpdateID + 1
			if update.Message == nil {
				continue
			}
			b.handle(ctx, update.Message)
		}
	}
}

func (b *Bot) handle(ctx context.Context, message *telegram.Message) {
	if message.Chat.ID != b.allowed {
		return
	}
	text := strings.TrimSpace(message.Text)
	if !strings.HasPrefix(text, "/") {
		return
	}

	fields := strings.Fields(text)
	command := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	if index := strings.Index(command, "@"); index > 0 {
		command = command[:index]
	}
	args := fields[1:]
	logx.Infof("команда /%s, аргументов %d", command, len(args))

	switch command {
	case "start", "help":
		b.reply(ctx, message.Chat.ID, helpText)
	case "list":
		b.list(ctx, message.Chat.ID)
	case "status":
		b.status(ctx, message.Chat.ID)
	case "check":
		b.check(ctx, message.Chat.ID)
	case "add":
		b.add(ctx, message, args)
	case "subscribe":
		b.subscribe(ctx, message.Chat.ID, args, true)
	case "unsubscribe":
		b.subscribe(ctx, message.Chat.ID, args, false)
	default:
		b.reply(ctx, message.Chat.ID, "не знаю такую команду. /help")
	}
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string) {
	if _, err := b.api.SendMessage(ctx, chatID, text); err != nil {
		logx.Errorf("telegram: %v", err)
	}
}

// ── commands ───────────────────────────────────────────────────────────────────────────────────

func (b *Bot) list(ctx context.Context, chatID int64) {
	titles, err := b.st.Titles(ctx)
	if err != nil {
		b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
		return
	}
	if len(titles) == 0 {
		b.reply(ctx, chatID, "в базе пока пусто")
		return
	}

	var out strings.Builder
	for _, title := range titles {
		mark := ""
		if title.Subscription != nil && title.Subscription.Enabled {
			mark = " 🔔"
		}
		fmt.Fprintf(&out, "<b>%s</b>%s\n", html.EscapeString(title.Title), mark)
		if len(title.Volumes) == 0 {
			out.WriteString("  томов нет\n")
			continue
		}
		parts := make([]string, 0, len(title.Volumes))
		for _, volume := range title.Volumes {
			label := volume.Label
			if volume.Status == store.StatusImported {
				label += "✓"
			}
			parts = append(parts, label)
		}
		fmt.Fprintf(&out, "  тома: %s\n", html.EscapeString(strings.Join(parts, ", ")))
	}
	b.reply(ctx, chatID, out.String())
}

func (b *Bot) status(ctx context.Context, chatID int64) {
	stats, err := b.st.Stats(ctx)
	if err != nil {
		b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("тайтлов: %d\nтомов: %d (импортировано %d)\nподписок: %d",
		stats.Titles, stats.Volumes, stats.Imported, stats.Subscriptions))
}

func (b *Bot) check(ctx context.Context, chatID int64) {
	subscriptions, err := b.st.Subscriptions(ctx)
	if err != nil {
		b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
		return
	}
	if len(subscriptions) == 0 {
		b.reply(ctx, chatID, "подписок нет")
		return
	}

	logx.Infof("проверка подписок: %d", len(subscriptions))
	var out strings.Builder
	total := 0
	for _, title := range subscriptions {
		chapters, err := b.src.Chapters(ctx, title.Slug)
		if err != nil {
			fmt.Fprintf(&out, "%s: ошибка %s\n", html.EscapeString(title.Title), html.EscapeString(err.Error()))
			continue
		}
		volumes := build.GroupVolumes(chapters)
		known, err := b.st.VolumeLabels(ctx, title.ID)
		if err != nil {
			b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
			return
		}

		var fresh []string
		for _, volume := range volumes {
			if _, ok := known[volume.Label]; !ok {
				fresh = append(fresh, volume.Label)
			}
		}
		last := ""
		if len(volumes) > 0 {
			last = volumes[len(volumes)-1].Label
		}
		_ = b.st.MarkChecked(ctx, title.ID, last)

		if len(fresh) == 0 {
			fmt.Fprintf(&out, "%s: новых томов нет\n", html.EscapeString(title.Title))
			continue
		}
		total += len(fresh)
		fmt.Fprintf(&out, "%s: новые тома %s\n", html.EscapeString(title.Title), html.EscapeString(strings.Join(fresh, ", ")))
	}
	if total > 0 {
		fmt.Fprintf(&out, "\nвсего новых: %d", total)
	}
	b.reply(ctx, chatID, out.String())
}

func (b *Bot) add(ctx context.Context, message *telegram.Message, args []string) {
	if len(args) == 0 {
		b.reply(ctx, message.Chat.ID, "нужна ссылка или slug: /add &lt;ссылка&gt; [том]")
		return
	}
	if b.cfg.BookDock == "" {
		b.reply(ctx, message.Chat.ID, "book_dock не настроен в конфиге")
		return
	}

	slug := parseSlug(args[0])
	volumes := ""
	all := false
	if len(args) > 1 {
		if strings.EqualFold(args[1], "all") {
			all = true
		} else {
			volumes = args[1]
		}
	}

	logx.Infof("в очередь: %s, тома %q, все %v", slug, volumes, all)
	messageID, err := b.api.SendMessage(ctx, message.Chat.ID, "ставлю в очередь: "+html.EscapeString(slug))
	if err != nil {
		return
	}
	select {
	case b.jobs <- job{chatID: message.Chat.ID, messageID: messageID, slug: slug, volumes: volumes, all: all}:
	default:
		_ = b.api.EditMessage(ctx, message.Chat.ID, messageID, "очередь переполнена, попробуй позже")
	}
}

func (b *Bot) subscribe(ctx context.Context, chatID int64, args []string, subscribe bool) {
	if len(args) == 0 {
		b.reply(ctx, chatID, "нужна ссылка или slug")
		return
	}
	slug := parseSlug(args[0])

	title, err := b.src.Title(ctx, slug)
	if err != nil {
		b.reply(ctx, chatID, "не нашёл: "+html.EscapeString(err.Error()))
		return
	}
	chapters, err := b.src.Chapters(ctx, title.Slug)
	if err != nil {
		b.reply(ctx, chatID, "ошибка: "+html.EscapeString(err.Error()))
		return
	}
	volumes := build.GroupVolumes(chapters)
	id, err := b.st.UpsertTitle(ctx, store.Title{
		Source:        b.src.Name(),
		Slug:          title.Slug,
		Title:         title.Title,
		Authors:       title.Authors,
		CoverURL:      title.CoverURL,
		SourceURL:     title.SourceURL,
		TotalChapters: len(chapters),
		TotalVolumes:  len(volumes),
	})
	if err != nil {
		b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
		return
	}

	if !subscribe {
		if err := b.st.Unsubscribe(ctx, id); err != nil {
			b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
			return
		}
		b.reply(ctx, chatID, "подписка снята: "+html.EscapeString(title.Title))
		return
	}

	hours := 24
	if len(args) > 1 {
		if parsed, err := strconv.Atoi(args[1]); err == nil && parsed > 0 {
			hours = parsed
		}
	}
	if err := b.st.Subscribe(ctx, id, hours); err != nil {
		b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("слежу за «%s», проверка каждые %d ч", html.EscapeString(title.Title), hours))
}

// ── worker ────────────────────────────────────────────────────────────────────────────────────

func (b *Bot) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-b.jobs:
			b.runJob(ctx, task)
		}
	}
}

func (b *Bot) runJob(ctx context.Context, task job) {
	edit := func(text string) {
		_ = b.api.EditMessage(ctx, task.chatID, task.messageID, text)
	}

	logx.Infof("задача: %s", task.slug)
	title, err := b.src.Title(ctx, task.slug)
	if err != nil {
		logx.Errorf("задача %s: %v", task.slug, err)
		edit("не нашёл: " + html.EscapeString(err.Error()))
		return
	}
	chapters, err := b.src.Chapters(ctx, title.Slug)
	if err != nil {
		edit("ошибка: " + html.EscapeString(err.Error()))
		return
	}
	volumes := build.GroupVolumes(chapters)
	if len(volumes) == 0 {
		edit("у тайтла нет глав")
		return
	}

	selected, err := selectVolumes(volumes, task.volumes, task.all)
	if err != nil {
		edit(html.EscapeString(err.Error()))
		return
	}

	titleID, err := b.st.UpsertTitle(ctx, store.Title{
		Source:        b.src.Name(),
		Slug:          title.Slug,
		Title:         title.Title,
		Authors:       title.Authors,
		CoverURL:      title.CoverURL,
		SourceURL:     title.SourceURL,
		TotalChapters: len(chapters),
		TotalVolumes:  len(volumes),
	})
	if err != nil {
		edit("ошибка базы: " + html.EscapeString(err.Error()))
		return
	}

	covers, err := b.src.VolumeCovers(ctx, title.Slug)
	if err != nil {
		covers = map[string]string{}
	}

	lastEdit := time.Now()
	progress := func(format string, args ...any) {
		if time.Since(lastEdit) < 3*time.Second {
			return
		}
		lastEdit = time.Now()
		edit(fmt.Sprintf("<b>%s</b>\n%s", html.EscapeString(title.Title), html.EscapeString(fmt.Sprintf(format, args...))))
	}

	logx.Infof("сборка «%s»: тома %s", title.Title, volumeLabels(selected))
	results, err := pipeline.Run(ctx, pipeline.Options{
		Source:        b.src,
		Store:         b.st,
		Title:         title,
		TitleID:       titleID,
		Volumes:       selected,
		Covers:        covers,
		Dock:          b.cfg.BookDock,
		IncludeImages: b.cfg.ImagesEnabled(),
		WaitForDock:   false,
		Progress:      progress,
	})

	var out strings.Builder
	fmt.Fprintf(&out, "<b>%s</b>\n", html.EscapeString(title.Title))
	for _, result := range results {
		fmt.Fprintf(&out, "%s: %d глав, %s\n", html.EscapeString(build.VolumeTitle(result.Volume.Label)),
			len(result.Volume.Chapters), humanSize(result.Size))
		if result.Imported {
			out.WriteString("  импортирован в BookOrbit\n")
		} else {
			out.WriteString("  в book-dock, ждёт подтверждения\n")
		}
	}
	if err != nil {
		logx.Errorf("сборка «%s»: %v", title.Title, err)
		fmt.Fprintf(&out, "\nошибка: %s", html.EscapeString(err.Error()))
	} else {
		logx.Infof("сборка «%s»: готово, томов %d", title.Title, len(results))
	}
	edit(out.String())
}

func volumeLabels(volumes []build.Volume) string {
	labels := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		labels = append(labels, volume.Label)
	}
	return strings.Join(labels, ",")
}

func selectVolumes(volumes []build.Volume, list string, all bool) ([]build.Volume, error) {
	if all || strings.TrimSpace(list) == "" {
		if all {
			return volumes, nil
		}
		return volumes[:1], nil
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

// parseSlug accepts a bare slug or any RanobeLib URL that contains one.
func parseSlug(input string) string {
	input = strings.TrimSpace(input)
	if !strings.Contains(input, "/") {
		return input
	}
	parts := strings.Split(strings.Trim(input, "/"), "/")
	for i, part := range parts {
		if part == "book" && i+1 < len(parts) {
			return strings.Split(parts[i+1], "?")[0]
		}
	}
	return strings.Split(parts[len(parts)-1], "?")[0]
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
