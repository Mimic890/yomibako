// Package bot is the Telegram front end: it lists what is tracked, starts downloads, and reports
// new volumes. Downloads run one at a time in a worker so the source is never hammered.
package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mimic890/yomibako/internal/build"
	"github.com/Mimic890/yomibako/internal/check"
	"github.com/Mimic890/yomibako/internal/config"
	"github.com/Mimic890/yomibako/internal/cookies"
	"github.com/Mimic890/yomibako/internal/logx"
	"github.com/Mimic890/yomibako/internal/pipeline"
	"github.com/Mimic890/yomibako/internal/reconcile"
	"github.com/Mimic890/yomibako/internal/source"
	"github.com/Mimic890/yomibako/internal/sources"
	"github.com/Mimic890/yomibako/internal/store"
	"github.com/Mimic890/yomibako/internal/telegram"
	"github.com/Mimic890/yomibako/internal/version"
)

// Bot serves one chat.
type Bot struct {
	api      *telegram.Client
	cfg      config.Config
	registry *sources.Registry
	st       *store.Store
	allowed  int64

	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
	pending  map[string]pendingJob
	picks    map[string]pick
	tasks    []*task
	busy     map[string]bool
	flow     map[int64][]int64
	deletes  map[string]deleteTarget
	queueMsg map[int64]int64
	watching map[int64]bool
}

// deleteTarget is a title waiting for the user to confirm its removal.
type deleteTarget struct {
	titleID int64
	title   string
	created time.Time
}

// pick is a slug waiting for the user to say which site it belongs to.
type pick struct {
	slug    string
	created time.Time
}

// pendingJob is a build waiting for confirmation. It is kept in memory: a confirmation that
// outlives a restart is not worth persisting.
type pendingJob struct {
	source   string
	slug     string
	volumes  []string
	chapters []string
	all      bool
	created  time.Time
}

// task is one build: it waits in the queue, then runs on its own source's worker. Titles of
// different sources build in parallel, titles of one source go one after another, so no site is
// hit by several downloads at once.
type task struct {
	id        string
	chatID    int64
	messageID int64
	source    string
	slug      string
	volumes   string
	chapters  []string
	all       bool

	title    string
	status   string
	err      string
	volDone  int
	volTotal int
	chDone   int
	chTotal  int
	chBase   int
	created  time.Time
	started  time.Time
	ended    time.Time
}

// New builds a bot from the configuration.
func New(cfg config.Config, registry *sources.Registry, st *store.Store) *Bot {
	return &Bot{
		api:      telegram.New(cfg.Telegram.Token),
		cfg:      cfg,
		registry: registry,
		st:       st,
		allowed:  cfg.Telegram.ChatID,
		pending:  make(map[string]pendingJob),
		picks:    make(map[string]pick),
		busy:     make(map[string]bool),
		cancels:  make(map[string]context.CancelFunc),
		flow:     make(map[int64][]int64),
		deletes:  make(map[string]deleteTarget),
		queueMsg: make(map[int64]int64),
		watching: make(map[int64]bool),
	}
}

// help is the /start message: what the service reads from, and what it can do.
func (b *Bot) help() string {
	return fmt.Sprintf(`<b>Yomibako</b>

Источники ранобэ: %s
Источники манги и манхвы: %s

/add &lt;ссылка&gt; - выбрать тома или главы и собрать
/check [ссылка] - новые тома и главы; без ссылки завершённые пропускаются
/sync - сверить базу с библиотекой
/list - что уже в базе
/subscribe &lt;ссылка&gt; - следить за тайтлом (проверка по /check)
/unsubscribe &lt;ссылка&gt; - перестать следить
/queue - что качается и что в очереди
/delete [ссылка] - удалить тайтл из базы и дока
/status - сводка
/cancel - прервать текущую сборку
/cookie - куки: состояние и кнопки, по какой ссылке их присылать
/cookie_clear - стереть куки

Тайтл выбираешь ты: сервису нужна ссылка на книгу, поиска он больше не делает.

Куки: просто отправь сюда файл cookies.txt - он вольётся в общий и заработает сразу.`,
		b.sourceList(source.KindNovel), b.sourceList(source.KindManga))
}

// sourceList renders the labels of the sources of one kind.
func (b *Bot) sourceList(kind source.Kind) string {
	var list []source.Source
	if kind == source.KindNovel {
		list = b.registry.Novels()
	} else {
		list = b.registry.Manga()
	}
	labels := make([]string, 0, len(list))
	for _, src := range list {
		labels = append(labels, b.registry.Label(src.Name()))
	}
	return strings.Join(labels, ", ")
}

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
	logx.Infof("бот @%s запущен, версия %s, чат %d", me.Username, version.Version, b.allowed)
	// A restart kills a running build, so say so instead of leaving the chat silent.
	if _, err := b.api.SendMessage(ctx, b.allowed, "бот перезапущен"); err != nil {
		logx.Warnf("не удалось сообщить о запуске: %v", err)
	}

	if err := b.api.SetMyCommands(ctx, [][2]string{
		{"list", "что уже в базе"},
		{"add", "собрать том или главу и отдать в BookOrbit"},
		{"check", "новые тома и главы"},
		{"sync", "сверить базу с библиотекой"},
		{"subscribe", "следить за тайтлом"},
		{"unsubscribe", "перестать следить"},
		{"queue", "что качается и что в очереди"},
		{"delete", "удалить тайтл из базы и дока"},
		{"status", "сводка"},
		{"cancel", "прервать текущую сборку"},
		{"cookie", "куки: состояние и обновление"},
		{"cookie_clear", "стереть куки"},
		{"help", "справка"},
	}); err != nil {
		logx.Warnf("меню команд не обновилось: %v", err)
	}

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
			switch {
			case update.Message != nil:
				b.handle(ctx, update.Message)
			case update.CallbackQuery != nil:
				b.handleCallback(ctx, update.CallbackQuery)
			}
		}
	}
}

func (b *Bot) handle(ctx context.Context, message *telegram.Message) {
	if message.Chat.ID != b.allowed {
		return
	}
	// A file drop is how cookies get refreshed: export from the browser, send here.
	if message.Document != nil {
		b.saveCookies(ctx, message.Chat.ID, message.Document)
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
		b.reply(ctx, message.Chat.ID, b.help())
	case "list":
		b.list(ctx, message.Chat.ID)
	case "status":
		b.status(ctx, message.Chat.ID)
	case "check":
		b.check(ctx, message.Chat.ID, args)
	case "sync":
		b.sync(ctx, message.Chat.ID)
	case "cancel":
		b.cancelJob(ctx, message.Chat.ID)
	case "cookie":
		b.cookie(ctx, message.Chat.ID, args)
	case "delete":
		b.deleteTitle(ctx, message.Chat.ID, args)
	case "queue":
		b.showQueue(ctx, message.Chat.ID)
	case "cookie_clear", "cookie-clear":
		b.clearCookies(ctx, message.Chat.ID)
	case "add":
		b.add(ctx, message.Chat.ID, args)
	case "subscribe":
		b.subscribe(ctx, message.Chat.ID, args, true)
	case "unsubscribe":
		b.subscribe(ctx, message.Chat.ID, args, false)
	default:
		b.reply(ctx, message.Chat.ID, "не знаю такую команду. /help")
	}
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string) {
	// Telegram refuses anything over 4096 characters, and a long list of titles gets there, so
	// the text is cut on line boundaries.
	for _, part := range splitMessage(text, 4000) {
		if _, err := b.api.SendMessage(ctx, chatID, part); err != nil {
			logx.Errorf("telegram: %v", err)
		}
	}
}

// splitMessage cuts text into pieces no longer than limit, preferring line breaks.
func splitMessage(text string, limit int) []string {
	if len(text) <= limit {
		return []string{text}
	}
	var parts []string
	for len(text) > limit {
		cut := strings.LastIndex(text[:limit], "\n")
		if cut <= 0 {
			cut = limit
		}
		parts = append(parts, text[:cut])
		text = strings.TrimLeft(text[cut:], "\n")
	}
	if text != "" {
		parts = append(parts, text)
	}
	return parts
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

	// Finished titles come last: they will not get new chapters, so they need no attention.
	sort.SliceStable(titles, func(i, j int) bool {
		a, b2 := check.Completed(titles[i].Status), check.Completed(titles[j].Status)
		if a != b2 {
			return !a
		}
		return strings.ToLower(titles[i].Title) < strings.ToLower(titles[j].Title)
	})

	items := make([]map[string]any, 0, len(titles))
	var fallback strings.Builder
	for _, title := range titles {
		ready, serverChapters := 0, 0
		for _, volume := range title.Volumes {
			if volume.Status == store.StatusImported || volume.Status == store.StatusBuilt {
				ready++
			}
			serverChapters += volume.Chapters
		}

		totalUnits, siteChapters, variants := title.TotalVolumes, title.TotalChapters, 0
		if src, ok := b.registry.ByName(title.Source); ok {
			if chapters, err := src.Chapters(ctx, title.Slug); err == nil {
				siteChapters = len(chapters)
				{
					groups, _ := build.PlanVolumes(src.Kind(), chapters)
					totalUnits = len(groups)
					variants = build.Variants(chapters)
					// A volume adopted from the library has no chapter count; fill it in while
					// the list is being built, so the numbers become right on their own.
					onSite := make(map[string]int, len(groups))
					for _, group := range groups {
						onSite[group.Label] = len(group.Chapters)
					}
					for _, volume := range title.Volumes {
						if volume.Chapters == 0 && onSite[volume.Label] > 0 {
							_ = b.st.SetVolumeChapters(ctx, title.ID, volume.Label, onSite[volume.Label])
							serverChapters += onSite[volume.Label]
						}
					}
				}
			}
		}

		// Both kinds are delivered as volumes: a novel by its own volumes, a comic cut into
		// tens. Counting chapters for comics made the list claim more volumes than exist.
		unitName := "томов"
		chaptersLabel := fmt.Sprintf("%d/%d глав", serverChapters, siteChapters)
		if variants > siteChapters {
			chaptersLabel += fmt.Sprintf(" (%d с переводами)", variants)
		}

		link := map[string]any{"type": "url", "url": title.SourceURL, "text": title.Title}
		text := []any{
			link,
			" · ",
			fmt.Sprintf("%d/%d %s", ready, totalUnits, unitName),
			" · ",
			chaptersLabel,
		}
		items = append(items, map[string]any{
			"blocks": []any{map[string]any{"type": "paragraph", "text": text}},
		})

		fmt.Fprintf(&fallback, `<a href="%s">%s</a> · %d/%d %s · %s`+"\n",
			html.EscapeString(title.SourceURL), html.EscapeString(title.Title), ready, totalUnits, unitName, html.EscapeString(chaptersLabel))
	}

	blocks := []map[string]any{{"type": "list", "items": items}}
	if _, err := b.api.SendRichMessage(ctx, chatID, blocks); err != nil {
		logx.Warnf("rich message не принят (%v), отправляю обычным", err)
		b.reply(ctx, chatID, fallback.String())
	}
}

func (b *Bot) status(ctx context.Context, chatID int64) {
	stats, err := b.st.Stats(ctx)
	if err != nil {
		b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
		return
	}
	text := fmt.Sprintf("тайтлов: %d\nтомов и глав: %d (импортировано %d)\nподписок: %d",
		stats.Titles, stats.Volumes, stats.Imported, stats.Subscriptions)
	if stats.Missing > 0 {
		text += fmt.Sprintf("\nнет в библиотеке: %d", stats.Missing)
	}
	text += "\n\nверсия " + version.Version
	b.reply(ctx, chatID, text)
}

// check looks for new volumes and chapters. Without an argument it walks the subscriptions and
// leaves finished titles alone; with a link it checks exactly that title, finished or not.
func (b *Bot) check(ctx context.Context, chatID int64, args []string) {
	resolve := func(title store.Title) (source.Source, bool) {
		return b.registry.ByName(title.Source)
	}

	var reports []check.TitleReport
	if len(args) > 0 {
		src, slug, err := b.registry.Resolve(args[0])
		if err != nil {
			b.reply(ctx, chatID, html.EscapeString(err.Error()))
			return
		}
		info, err := src.Title(ctx, slug)
		if err != nil {
			b.reply(ctx, chatID, "не нашёл: "+html.EscapeString(err.Error()))
			return
		}
		id, err := b.st.UpsertTitle(ctx, store.Title{
			Source: src.Name(), Slug: info.Slug, Title: info.Title, Authors: info.Authors,
			CoverURL: info.CoverURL, SourceURL: info.SourceURL, Status: info.Status,
			Kind: string(src.Kind()),
		})
		if err != nil {
			b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
			return
		}
		reports = append(reports, check.Check(ctx, b.st, resolve, store.Title{
			ID: id, Source: src.Name(), Slug: info.Slug, Title: info.Title, Status: info.Status,
		}))
	} else {
		var err error
		reports, err = check.Run(ctx, b.st, resolve, check.Options{SkipCompleted: true})
		if err != nil {
			b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
			return
		}
		if len(reports) == 0 {
			b.reply(ctx, chatID, "подписок нет")
			return
		}
	}

	var out strings.Builder
	newUnits, updated, skipped := 0, 0, 0
	for _, report := range reports {
		if report.Skipped {
			skipped++
			continue
		}
		name := html.EscapeString(report.Title.Title)
		if report.Err != nil {
			fmt.Fprintf(&out, "%s: ошибка %s\n", name, html.EscapeString(report.Err.Error()))
			continue
		}
		if len(report.New) == 0 && len(report.Updated) == 0 {
			fmt.Fprintf(&out, "%s: без изменений\n", name)
			continue
		}
		fmt.Fprintf(&out, "<b>%s</b>\n", name)
		for _, volume := range report.New {
			newUnits++
			fmt.Fprintf(&out, "  новый том %s (%d глав)\n", html.EscapeString(volume.Label), len(volume.Chapters))
		}
		for _, update := range report.Updated {
			updated++
			fmt.Fprintf(&out, "  том %s: %d глав из %d\n", html.EscapeString(update.Label), update.Have, update.Site)
		}
	}
	if skipped > 0 {
		fmt.Fprintf(&out, "завершённых пропущено: %d (проверить: /check &lt;ссылка&gt;)\n", skipped)
	}
	if newUnits == 0 && updated == 0 {
		out.WriteString("\nнового нет")
	} else {
		fmt.Fprintf(&out, "\nновых томов: %d, томов с новыми главами: %d", newUnits, updated)
	}
	b.reply(ctx, chatID, out.String())
}

func (b *Bot) sync(ctx context.Context, chatID int64) {
	report, err := reconcile.Run(ctx, b.st, b.cfg.LibraryDir)
	if err != nil {
		b.reply(ctx, chatID, "ошибка: "+html.EscapeString(err.Error()))
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("сверено с библиотекой\nимпортировано: %d\nнет на диске: %d\nдобавлено из библиотеки: %d",
		report.Imported, report.Missing, report.Adopted))
}

func (b *Bot) cancelJob(ctx context.Context, chatID int64) {
	b.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(b.cancels))
	for _, cancel := range b.cancels {
		cancels = append(cancels, cancel)
	}
	b.mu.Unlock()
	if len(cancels) == 0 {
		b.reply(ctx, chatID, "нечего отменять")
		return
	}
	for _, cancel := range cancels {
		cancel()
	}
	logx.Infof("отмена сборок: %d", len(cancels))
	b.reply(ctx, chatID, fmt.Sprintf("отменяю: %d", len(cancels)))
}

// saveCookies stores an uploaded cookies.txt, merges it with what is already there and switches the
// sources over to the new jar without a restart.
func (b *Bot) saveCookies(ctx context.Context, chatID int64, document *telegram.Document) {
	if b.cfg.Auth.CookiesFile == "" {
		b.reply(ctx, chatID, "в конфиге не задан auth.cookies_file, сохранять некуда")
		return
	}
	if document.FileSize > 2<<20 {
		b.reply(ctx, chatID, "файл слишком большой для cookies.txt")
		return
	}
	if name := strings.ToLower(document.FileName); name != "" && !strings.HasSuffix(name, ".txt") {
		b.reply(ctx, chatID, "нужен файл cookies.txt в формате Netscape")
		return
	}

	body, err := b.api.Download(ctx, document.FileID)
	if err != nil {
		b.reply(ctx, chatID, "не смог скачать файл: "+html.EscapeString(err.Error()))
		return
	}
	jar, err := cookies.Merge(b.cfg.Auth.CookiesFile, body)
	if err != nil {
		b.reply(ctx, chatID, html.EscapeString(err.Error()))
		return
	}

	b.registry.ReloadCookies(jar)
	logx.Infof("куки обновлены из бота, доменов %d", len(jar))
	b.reply(ctx, chatID, b.cookieSummary(jar, "Куки обновлены:"))
}

// cookie reports which sites are authorised. Arguments are accepted as a RanobeLib token for the
// case where cookies alone are not enough, but the normal way is a file drop.
func (b *Bot) cookie(ctx context.Context, chatID int64, args []string) {
	if len(args) == 0 {
		jar, err := cookies.Load(b.cfg.Auth.CookiesFile)
		if err != nil {
			b.reply(ctx, chatID, html.EscapeString(err.Error()))
			return
		}
		if _, err := b.api.SendMessageWithKeyboard(ctx, chatID, b.cookieSummary(jar, "Куки по источникам:"),
			b.cookieButtons()); err != nil {
			logx.Errorf("telegram: %v", err)
		}
		return
	}
	token := strings.TrimSpace(args[0])
	if len(token) < 20 {
		b.reply(ctx, chatID, "это не похоже на токен")
		return
	}
	if err := b.st.SetSetting(ctx, "ranobelib_token", token); err != nil {
		b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
		return
	}
	b.registry.SetToken(token)
	logx.Infof("токен обновлён")
	b.reply(ctx, chatID, "токен обновлён")
}

// cookieStatus lists the sources and whether each has cookies. A full export holds hundreds of
// domains and would not fit in a message.
func (b *Bot) cookieStatus() string {
	if b.cfg.Auth.CookiesFile == "" {
		return "auth.cookies_file не задан в конфиге: куки складывать некуда"
	}
	jar, err := cookies.Load(b.cfg.Auth.CookiesFile)
	if err != nil {
		return "куки не читаются: " + html.EscapeString(err.Error())
	}
	return b.cookieSummary(jar, "Куки по источникам:") + "\n\nЧтобы обновить - отправь файл cookies.txt в чат."
}

// cookieButtons offers every way to send cookies: the whole export, or one site at a time. Telegram
// has no file picker to open from a button, so a button answers with what exactly to send.
func (b *Bot) cookieButtons() [][]telegram.Button {
	rows := [][]telegram.Button{{{Text: "Все куки", Data: "ck:all"}}}
	for _, src := range b.registry.All() {
		rows = append(rows, []telegram.Button{{Text: b.registry.Label(src.Name()), Data: "ck:" + src.Name()}})
	}
	return rows
}

// cookieHelp answers a button with the file to send for it.
func (b *Bot) cookieHelp(ctx context.Context, chatID int64, which string) {
	if which == "all" {
		b.reply(ctx, chatID, "Кидай сюда файл cookies.txt целиком. Вольётся в общий файл: домены, которые есть "+
			"в новом экспорте, заменятся, остальные останутся.")
		return
	}
	host := b.registry.Host(which)
	if _, ok := b.registry.ByName(which); !ok || host == "" {
		b.reply(ctx, chatID, "неизвестный источник")
		return
	}
	b.reply(ctx, chatID, fmt.Sprintf("Кидай файл с куками для <b>%s</b> (%s). Остальные сайты не тронутся.",
		html.EscapeString(b.registry.Label(which)), html.EscapeString(host)))
}

// clearCookies wipes the jar and leaves every source without cookies.
func (b *Bot) clearCookies(ctx context.Context, chatID int64) {
	if b.cfg.Auth.CookiesFile == "" {
		b.reply(ctx, chatID, "auth.cookies_file не задан в конфиге")
		return
	}
	count := 0
	if jar, err := cookies.Load(b.cfg.Auth.CookiesFile); err == nil {
		count = len(jar)
	}
	if err := cookies.Clear(b.cfg.Auth.CookiesFile); err != nil {
		b.reply(ctx, chatID, html.EscapeString(err.Error()))
		return
	}
	b.registry.ReloadCookies(cookies.Jar{})
	logx.Infof("куки стёрты из бота, было доменов %d", count)
	b.reply(ctx, chatID, fmt.Sprintf("куки стёрты (было доменов: %d)", count))
}

// cookieSummary renders the per-source state of a jar.
func (b *Bot) cookieSummary(jar cookies.Jar, header string) string {
	var out strings.Builder
	out.WriteString(header)
	for _, entry := range b.registry.CookieStatus(jar) {
		mark := "✗"
		if entry.Has {
			mark = "✓"
		}
		fmt.Fprintf(&out, "\n%s %s (%s)", mark, html.EscapeString(entry.Label), html.EscapeString(entry.Host))
	}
	fmt.Fprintf(&out, "\n\nдоменов в файле: %d", len(jar))
	return out.String()
}

func (b *Bot) add(ctx context.Context, chatID int64, args []string) {
	if len(args) == 0 {
		b.reply(ctx, chatID, "нужна ссылка на тайтл: /add &lt;ссылка&gt;")
		return
	}
	if b.cfg.BookDock == "" {
		b.reply(ctx, chatID, "book_dock не настроен в конфиге")
		return
	}

	link := strings.Join(args, " ")
	src, slug, err := b.registry.Resolve(link)
	if errors.Is(err, sources.ErrNotURL) {
		// Not a link: the user typed a slug, so ask which site it belongs to.
		b.askSource(ctx, chatID, link)
		return
	}
	if err != nil {
		b.reply(ctx, chatID, html.EscapeString(err.Error()))
		return
	}
	b.showUnits(ctx, chatID, src, slug)
}

// askSource turns a bare slug into a question: the same slug may exist on several sites, and only
// the user knows which one was meant.
func (b *Bot) askSource(ctx context.Context, chatID int64, slug string) {
	token := b.rememberPick(slug)
	rows := make([][]telegram.Button, 0, 5)
	for _, src := range b.registry.All() {
		rows = append(rows, []telegram.Button{{
			Text: b.registry.Label(src.Name()),
			Data: "s:" + src.Name() + ":" + token,
		}})
	}
	text := fmt.Sprintf("С какого сайта взять «%s»?", html.EscapeString(slug))
	b.flowMessage(ctx, chatID, text, rows)
}

// showUnits lists the volumes of a title. Manga is built one CBZ per volume too: BookOrbit makes
// one book out of one file, so a chapter-per-file run would land as a pile of separate titles.
func (b *Bot) showUnits(ctx context.Context, chatID int64, src source.Source, slug string) {
	b.showVolumes(ctx, chatID, src, slug)
}

func (b *Bot) showVolumes(ctx context.Context, chatID int64, src source.Source, slug string) {
	title, err := src.Title(ctx, slug)
	if err != nil {
		b.reply(ctx, chatID, "не нашёл: "+html.EscapeString(err.Error()))
		return
	}
	chapters, err := src.Chapters(ctx, title.Slug)
	if err != nil {
		b.reply(ctx, chatID, "ошибка: "+html.EscapeString(err.Error()))
		return
	}
	volumes, err := build.PlanVolumes(src.Kind(), chapters)
	if err != nil {
		b.reply(ctx, chatID, html.EscapeString(err.Error()))
		return
	}

	titleID, err := b.st.UpsertTitle(ctx, store.Title{
		Source:        src.Name(),
		Slug:          title.Slug,
		Title:         title.Title,
		Authors:       title.Authors,
		CoverURL:      title.CoverURL,
		SourceURL:     title.SourceURL,
		TotalChapters: len(chapters),
		TotalVolumes:  len(volumes),
		Kind:          string(src.Kind()),
		Status:        title.Status,
	})
	// Only volumes that are actually on disk get a tick: a volume marked missing was deleted from
	// the library and can be built again.
	known := map[string]string{}
	if err == nil {
		if labels, err := b.st.VolumeLabels(ctx, titleID); err == nil {
			for label, status := range labels {
				if status != store.StatusMissing {
					known[label] = status
				}
			}
		}
	}

	logx.Infof("%s: %d глав, %d томов", title.Slug, len(chapters), len(volumes))
	rows := b.volumeButtons(src.Name(), title.Slug, volumes, known)

	text := fmt.Sprintf("<b>%s</b>\nтомов: %d, глав: %d\n\nвыбери том:", html.EscapeString(title.Title), len(volumes), len(chapters))
	b.flowMessage(ctx, chatID, text, rows)
}

// volumeButtons lays out one button per volume, four per row, plus an "all" button.
func (b *Bot) volumeButtons(sourceName, slug string, volumes []build.Volume, known map[string]string) [][]telegram.Button {
	// Telegram caps callback data at 64 bytes and a com-x slug alone can pass a hundred, so the
	// buttons carry a short token instead of the slug.
	token := b.rememberPick(slug)
	var rows [][]telegram.Button
	var row []telegram.Button
	for _, volume := range volumes {
		label := build.VolumeTitle(volume.Label)
		if _, ok := known[volume.Label]; ok {
			label += " ✓"
		}
		row = append(row, telegram.Button{Text: label, Data: "v:" + sourceName + ":" + token + ":" + volume.Label})
		if len(row) == 4 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return append(rows, []telegram.Button{{Text: "Все тома", Data: "a:" + sourceName + ":" + token}})
}

// handleCallback reacts to an inline button.
func (b *Bot) handleCallback(ctx context.Context, query *telegram.CallbackQuery) {
	if query.Message == nil || query.Message.Chat.ID != b.allowed {
		return
	}
	chatID := query.Message.Chat.ID
	_ = b.api.AnswerCallback(ctx, query.ID, "")

	parts := strings.Split(query.Data, ":")
	switch parts[0] {
	case "t":
		if len(parts) == 3 {
			if src, ok := b.registry.ByName(parts[1]); ok {
				if slug, ok := b.peekPick(parts[2]); ok {
					b.showUnits(ctx, chatID, src, slug)
				}
			}
		}
	case "v":
		if len(parts) == 4 {
			if src, ok := b.registry.ByName(parts[1]); ok {
				if slug, ok := b.peekPick(parts[2]); ok {
					b.confirm(ctx, chatID, src, slug, []string{parts[3]}, false)
				}
			}
		}
	case "a":
		if len(parts) == 3 {
			if src, ok := b.registry.ByName(parts[1]); ok {
				if slug, ok := b.peekPick(parts[2]); ok {
					b.confirm(ctx, chatID, src, slug, nil, true)
				}
			}
		}
	case "del":
		if len(parts) == 2 {
			if item, ok := b.peekDelete(parts[1]); ok {
				b.confirmDelete(ctx, chatID, item.titleID, item.title)
			}
		}
	case "delok":
		if len(parts) == 2 {
			if item, ok := b.takeDelete(parts[1]); ok {
				b.removeTitle(ctx, chatID, item.titleID, item.title)
			}
		}
	case "delno":
		if len(parts) == 2 {
			b.takeDelete(parts[1])
			b.reply(ctx, chatID, "отмена")
		}
	case "ck":
		if len(parts) == 2 {
			b.cookieHelp(ctx, chatID, parts[1])
		}
	case "s":
		if len(parts) == 3 {
			slug, ok := b.takePick(parts[2])
			if !ok {
				b.reply(ctx, chatID, "выбор устарел, пришли ссылку заново")
				return
			}
			if src, found := b.registry.ByName(parts[1]); found {
				b.showUnits(ctx, chatID, src, slug)
			}
		}
	case "go":
		if len(parts) == 2 {
			b.startPending(ctx, chatID, parts[1])
		}
	}
}

// confirm shows what is about to be downloaded and waits for a button press.
func (b *Bot) confirm(ctx context.Context, chatID int64, src source.Source, slug string, units []string, all bool) {
	title, err := src.Title(ctx, slug)
	if err != nil {
		b.reply(ctx, chatID, "не нашёл: "+html.EscapeString(err.Error()))
		return
	}
	chapters, err := src.Chapters(ctx, title.Slug)
	if err != nil {
		b.reply(ctx, chatID, "ошибка: "+html.EscapeString(err.Error()))
		return
	}
	groups, err := build.PlanVolumes(src.Kind(), chapters)
	if err != nil {
		b.reply(ctx, chatID, html.EscapeString(err.Error()))
		return
	}

	titleID, _ := b.st.UpsertTitle(ctx, store.Title{
		Source:        src.Name(),
		Slug:          title.Slug,
		Title:         title.Title,
		Authors:       title.Authors,
		CoverURL:      title.CoverURL,
		SourceURL:     title.SourceURL,
		TotalChapters: len(chapters),
		TotalVolumes:  len(groups),
		Kind:          string(src.Kind()),
	})
	present := map[string]bool{}
	if labels, err := b.st.VolumeLabels(ctx, titleID); err == nil {
		for label, status := range labels {
			if status != store.StatusMissing {
				present[label] = true
			}
		}
	}

	var selected []build.Volume
	if all {
		for _, group := range groups {
			if !present[group.Label] {
				selected = append(selected, group)
			}
		}
	} else {
		for _, group := range groups {
			if len(units) > 0 && group.Label == units[0] {
				selected = append(selected, group)
			}
		}
	}

	if len(selected) == 0 {
		b.reply(ctx, chatID, "все тома уже в библиотеке")
		return
	}

	var out strings.Builder
	fmt.Fprintf(&out, "<b>%s</b>\n", html.EscapeString(title.Title))
	totalChapters := 0
	for i, group := range selected {
		if i == 12 {
			fmt.Fprintf(&out, "… и ещё %d томов\n", len(selected)-i)
			break
		}
		totalChapters += len(group.Chapters)
		fmt.Fprintf(&out, "%s · %d глав\n", html.EscapeString(build.VolumeTitle(group.Label)), len(group.Chapters))
	}
	if len(selected) <= 12 {
		fmt.Fprintf(&out, "\nвсего глав: %d", totalChapters)
	}
	if all && len(present) > 0 {
		fmt.Fprintf(&out, "\nуже в библиотеке, пропускаю: %d томов", len(present))
	}
	if !all && present[selected[0].Label] {
		out.WriteString("\n\nэтот том уже в библиотеке - собрать заново?")
	}

	labels := make([]string, 0, len(selected))
	for _, group := range selected {
		labels = append(labels, group.Label)
	}
	token := b.remember(pendingJob{source: src.Name(), slug: title.Slug, volumes: labels, all: all, created: time.Now()})
	// The back button cannot carry the slug: Telegram limits callback data to 64 bytes.
	back := b.rememberPick(title.Slug)

	rows := [][]telegram.Button{
		{{Text: "Подтвердить", Data: "go:" + token}},
		{{Text: "Назад", Data: "t:" + src.Name() + ":" + back}},
	}
	b.flowMessage(ctx, chatID, out.String(), rows)
}

// randomToken returns a short unguessable token for a pending confirmation.
func randomToken() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}

// remember stores a pending job and returns its token.
func (b *Bot) remember(job pendingJob) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Drop confirmations older than ten minutes.
	for token, item := range b.pending {
		if time.Since(item.created) > 10*time.Minute {
			delete(b.pending, token)
		}
	}
	token := randomToken()
	b.pending[token] = job
	return token
}

// rememberPick stores a slug and returns a token for the source buttons.
func (b *Bot) rememberPick(slug string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for token, item := range b.picks {
		if time.Since(item.created) > 10*time.Minute {
			delete(b.picks, token)
		}
	}
	token := randomToken()
	b.picks[token] = pick{slug: slug, created: time.Now()}
	return token
}

// peekPick returns the slug behind a token without consuming it: the volume buttons of one title
// are pressed more than once.
func (b *Bot) peekPick(token string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	item, ok := b.picks[token]
	if !ok || time.Since(item.created) > 10*time.Minute {
		return "", false
	}
	return item.slug, true
}

// takePick returns the slug behind a token and forgets it.
func (b *Bot) takePick(token string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	item, ok := b.picks[token]
	delete(b.picks, token)
	if !ok || time.Since(item.created) > 10*time.Minute {
		return "", false
	}
	return item.slug, true
}

// startPending enqueues a confirmed job.
func (b *Bot) startPending(ctx context.Context, chatID int64, token string) {
	b.mu.Lock()
	item, ok := b.pending[token]
	delete(b.pending, token)
	b.mu.Unlock()
	if !ok {
		b.reply(ctx, chatID, "подтверждение устарело, начни заново")
		return
	}
	// The confirmation already decided what to build, so the job carries the exact list.
	if len(item.chapters) > 0 {
		b.enqueueChapters(ctx, chatID, item.source, item.slug, item.chapters)
		return
	}
	b.enqueue(ctx, chatID, item.source, item.slug, strings.Join(item.volumes, ","), false)
}

// enqueue puts a build job on the queue and reports it in the chat.
func (b *Bot) enqueue(ctx context.Context, chatID int64, sourceName, slug, volumes string, all bool) {
	b.addTask(ctx, chatID, &task{source: sourceName, slug: slug, volumes: volumes, all: all})
}

// enqueueChapters puts a manga build on the queue.
func (b *Bot) enqueueChapters(ctx context.Context, chatID int64, sourceName, slug string, chapters []string) {
	b.addTask(ctx, chatID, &task{source: sourceName, slug: slug, chapters: chapters})
}

func (b *Bot) addTask(ctx context.Context, chatID int64, t *task) {
	t.id = randomToken()
	t.chatID = chatID
	t.created = time.Now()
	t.status = "queued"

	b.mu.Lock()
	for _, known := range b.tasks {
		if known.source == t.source && known.slug == t.slug &&
			(known.status == "queued" || known.status == "running") {
			b.mu.Unlock()
			b.reply(ctx, chatID, "такой тайтл уже в очереди")
			return
		}
	}
	position := 1 + len(b.tasks)
	b.tasks = append(b.tasks, t)
	b.mu.Unlock()

	b.clearFlow(ctx, chatID)
	logx.Infof("в очередь: %s (%s), позиция %d", t.slug, t.source, position)
	messageID, err := b.api.SendMessage(ctx, chatID, fmt.Sprintf("в очереди %d: %s", position,
		html.EscapeString(t.slug)))
	if err != nil {
		logx.Errorf("telegram: %v", err)
		return
	}
	t.messageID = messageID

	// A queue message already on screen should follow the new task too.
	b.mu.Lock()
	queueMessage, hasQueue := b.queueMsg[chatID]
	b.mu.Unlock()
	if hasQueue {
		b.watchQueue(ctx, chatID, queueMessage)
	}

	go b.dispatch(ctx)
}

// dispatch starts every queued task whose source is idle.
func (b *Bot) dispatch(ctx context.Context) {
	b.mu.Lock()
	start := nextTasks(b.tasks, b.busy)
	for _, t := range start {
		t.status = "running"
		t.started = time.Now()
	}
	b.mu.Unlock()

	for _, t := range start {
		go b.runTask(ctx, t)
	}
}

// nextTasks picks the tasks that may start now: one per source, in queue order. Titles of one site
// never run together, titles of different sites do.
func nextTasks(tasks []*task, busy map[string]bool) []*task {
	var start []*task
	for _, t := range tasks {
		if t.status != "queued" || busy[t.source] {
			continue
		}
		busy[t.source] = true
		start = append(start, t)
	}
	return start
}

func (b *Bot) subscribe(ctx context.Context, chatID int64, args []string, subscribe bool) {
	if len(args) == 0 {
		b.reply(ctx, chatID, "нужна ссылка на тайтл")
		return
	}
	src, slug, err := b.registry.Resolve(args[0])
	if err != nil {
		b.reply(ctx, chatID, html.EscapeString(err.Error()))
		return
	}

	title, err := src.Title(ctx, slug)
	if err != nil {
		b.reply(ctx, chatID, "не нашёл: "+html.EscapeString(err.Error()))
		return
	}
	chapters, err := src.Chapters(ctx, title.Slug)
	if err != nil {
		b.reply(ctx, chatID, "ошибка: "+html.EscapeString(err.Error()))
		return
	}
	volumes := build.GroupVolumes(chapters)
	id, err := b.st.UpsertTitle(ctx, store.Title{
		Source:        src.Name(),
		Slug:          title.Slug,
		Title:         title.Title,
		Authors:       title.Authors,
		CoverURL:      title.CoverURL,
		SourceURL:     title.SourceURL,
		TotalChapters: len(chapters),
		TotalVolumes:  len(volumes),
		Kind:          string(src.Kind()),
		Status:        title.Status,
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
	// Nothing runs on a timer: the subscription only marks the title for /check.
	b.reply(ctx, chatID, fmt.Sprintf("слежу за «%s» - новинки ищутся по /check", html.EscapeString(title.Title)))
}

// flowMessage sends a step of the /add dialogue and removes the previous step, so the chat does not
// fill up with dead volume lists.
func (b *Bot) flowMessage(ctx context.Context, chatID int64, text string, buttons [][]telegram.Button) {
	b.mu.Lock()
	previous := b.flow[chatID]
	b.flow[chatID] = nil
	b.mu.Unlock()
	for _, id := range previous {
		_ = b.api.DeleteMessage(ctx, chatID, id)
	}

	messageID, err := b.api.SendMessageWithKeyboard(ctx, chatID, text, buttons)
	if err != nil {
		logx.Errorf("telegram: %v", err)
		return
	}
	b.mu.Lock()
	b.flow[chatID] = append(b.flow[chatID], messageID)
	b.mu.Unlock()
}

// clearFlow removes the dialogue messages, called when the build starts.
func (b *Bot) clearFlow(ctx context.Context, chatID int64) {
	b.mu.Lock()
	previous := b.flow[chatID]
	b.flow[chatID] = nil
	b.mu.Unlock()
	for _, id := range previous {
		_ = b.api.DeleteMessage(ctx, chatID, id)
	}
}

// sourceLabel names a source for the chat, falling back to its key.
func (b *Bot) sourceLabel(name string) string {
	if b.registry == nil {
		return name
	}
	if label := b.registry.Label(name); label != "" {
		return label
	}
	return name
}

// sweep forgets finished tasks after half an hour, so the queue stays short.
func (b *Bot) sweep() {
	kept := b.tasks[:0]
	for _, t := range b.tasks {
		if t.status == "done" || t.status == "failed" {
			if time.Since(t.ended) > 30*time.Minute {
				continue
			}
		}
		kept = append(kept, t)
	}
	b.tasks = kept
}

// showQueue prints the queue and keeps the message up to date while anything is downloading.
func (b *Bot) showQueue(ctx context.Context, chatID int64) {
	text := b.queueText()

	b.mu.Lock()
	messageID, known := b.queueMsg[chatID]
	b.mu.Unlock()

	// The old message is replaced rather than edited: an edit leaves it where it was, far above
	// the new commands, and the user never sees the numbers change.
	if known {
		_ = b.api.DeleteMessage(ctx, chatID, messageID)
	}

	sent, err := b.api.SendMessage(ctx, chatID, text)
	if err != nil {
		logx.Errorf("telegram: %v", err)
		return
	}
	b.mu.Lock()
	b.queueMsg[chatID] = sent
	b.mu.Unlock()
	b.watchQueue(ctx, chatID, sent)
}

// watchQueue refreshes the queue message every few seconds until the queue drains.
func (b *Bot) watchQueue(ctx context.Context, chatID, messageID int64) {
	b.mu.Lock()
	if b.watching[chatID] {
		b.mu.Unlock()
		return
	}
	b.watching[chatID] = true
	b.mu.Unlock()

	go func() {
		defer func() {
			b.mu.Lock()
			b.watching[chatID] = false
			b.mu.Unlock()
		}()

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = b.api.EditMessage(ctx, chatID, messageID, b.queueText())
				if !b.hasActive() {
					return
				}
			}
		}
	}()
}

// hasActive tells whether anything is downloading or waiting.
func (b *Bot) hasActive() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range b.tasks {
		if t.status == "queued" || t.status == "running" {
			return true
		}
	}
	return false
}

// queueText renders the queue grouped by source: what is downloading now and what waits.
func (b *Bot) queueText() string {
	b.mu.Lock()
	tasks := make([]task, 0, len(b.tasks))
	for _, t := range b.tasks {
		tasks = append(tasks, *t)
	}
	b.mu.Unlock()

	if len(tasks) == 0 {
		return "очередь пуста"
	}

	bySource := make(map[string][]task)
	var order []string
	for _, t := range tasks {
		if _, ok := bySource[t.source]; !ok {
			order = append(order, t.source)
		}
		bySource[t.source] = append(bySource[t.source], t)
	}

	var out strings.Builder
	out.WriteString("<b>Статус загрузки:</b>\n")
	for _, name := range order {
		fmt.Fprintf(&out, "\n<b>%s:</b>\n", html.EscapeString(b.sourceLabel(name)))
		for _, t := range bySource[name] {
			title := t.title
			if title == "" {
				title = t.slug
			}
			fmt.Fprintf(&out, "- %s\n", html.EscapeString(title))
			switch t.status {
			case "queued":
				out.WriteString("  в очереди\n")
			case "running":
				fmt.Fprintf(&out, "  %d/%d томов · %d/%d глав\n", t.volDone, t.volTotal, t.chDone, t.chTotal)
			case "done":
				fmt.Fprintf(&out, "  готово: %d томов · %d глав\n", t.volDone, t.chDone)
			case "failed":
				fmt.Fprintf(&out, "  ошибка: %s\n", html.EscapeString(t.err))
			}
		}
	}
	return out.String()
}

// deleteTitle asks which title to forget, or confirms the one from the link.
func (b *Bot) deleteTitle(ctx context.Context, chatID int64, args []string) {
	if len(args) == 0 {
		titles, err := b.st.Titles(ctx)
		if err != nil {
			b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
			return
		}
		if len(titles) == 0 {
			b.reply(ctx, chatID, "в базе пусто")
			return
		}
		rows := make([][]telegram.Button, 0, len(titles))
		for _, title := range titles {
			token := b.rememberDelete(title.ID, title.Title)
			// One work can live on two sites, so the button names the source as well.
			label := fmt.Sprintf("%s · %s", title.Title, b.sourceLabel(title.Source))
			if len([]rune(label)) > 60 {
				label = string([]rune(label)[:59]) + "…"
			}
			rows = append(rows, []telegram.Button{{
				Text: label,
				Data: "del:" + token,
			}})
		}
		b.flowMessage(ctx, chatID, "Что удалить?", rows)
		return
	}

	src, slug, err := b.registry.Resolve(args[0])
	if err != nil {
		b.reply(ctx, chatID, html.EscapeString(err.Error()))
		return
	}
	title, err := b.st.FindTitle(ctx, src.Name(), slug)
	if err != nil {
		b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
		return
	}
	if title == nil {
		b.reply(ctx, chatID, "такого тайтла в базе нет")
		return
	}
	if b.inQueue(title.Source, title.Slug) {
		b.reply(ctx, chatID, "тайтл сейчас качается, сначала /cancel")
		return
	}
	b.confirmDelete(ctx, chatID, title.ID, title.Title)
}

// confirmDelete shows what will happen to the files before anything is removed.
func (b *Bot) confirmDelete(ctx context.Context, chatID int64, titleID int64, name string) {
	volumes, err := b.st.Volumes(ctx, titleID)
	if err != nil {
		b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
		return
	}
	inDock, imported := 0, 0
	for _, volume := range volumes {
		if volume.Status == store.StatusImported {
			imported++
			continue
		}
		if volume.FilePath != "" {
			inDock++
		}
	}

	text := fmt.Sprintf("<b>%s</b>\nтомов в доке: %d (файлы удалю)\nимпортировано: %d (файлы в библиотеке останутся)",
		html.EscapeString(name), inDock, imported)
	token := b.rememberDelete(titleID, name)
	b.flowMessage(ctx, chatID, text, [][]telegram.Button{
		{{Text: "Удалить", Data: "delok:" + token}},
		{{Text: "Отмена", Data: "delno:" + token}},
	})
}

// removeTitle deletes the dock files and forgets the title.
func (b *Bot) removeTitle(ctx context.Context, chatID int64, titleID int64, name string) {
	volumes, err := b.st.Volumes(ctx, titleID)
	if err != nil {
		b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
		return
	}

	removed := 0
	for _, volume := range volumes {
		if volume.Status == store.StatusImported || volume.FilePath == "" {
			continue
		}
		// Only files inside the dock are ours to delete; the library is read-only.
		if b.cfg.BookDock == "" || !strings.HasPrefix(volume.FilePath, b.cfg.BookDock) {
			continue
		}
		if err := os.Remove(volume.FilePath); err != nil && !os.IsNotExist(err) {
			logx.Warnf("удаление %s: %v", volume.FilePath, err)
			continue
		}
		removed++
	}

	if err := b.st.DeleteTitle(ctx, titleID); err != nil {
		b.reply(ctx, chatID, "ошибка базы: "+html.EscapeString(err.Error()))
		return
	}
	logx.Infof("тайтл удалён: %s, файлов %d", name, removed)
	b.reply(ctx, chatID, fmt.Sprintf("удалено: %s\nфайлов из дока: %d", html.EscapeString(name), removed))
}

// inQueue tells whether a title is being built or waits for its turn.
func (b *Bot) inQueue(sourceName, slug string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range b.tasks {
		if t.source == sourceName && t.slug == slug && (t.status == "queued" || t.status == "running") {
			return true
		}
	}
	return false
}

// rememberDelete stores a title waiting for confirmation.
func (b *Bot) rememberDelete(titleID int64, name string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for token, item := range b.deletes {
		if time.Since(item.created) > 10*time.Minute {
			delete(b.deletes, token)
		}
	}
	token := randomToken()
	b.deletes[token] = deleteTarget{titleID: titleID, title: name, created: time.Now()}
	return token
}

// peekDelete returns the title behind a token without consuming it.
func (b *Bot) peekDelete(token string) (deleteTarget, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	item, ok := b.deletes[token]
	if !ok || time.Since(item.created) > 10*time.Minute {
		return deleteTarget{}, false
	}
	return item, true
}

// takeDelete returns the title behind a token and forgets it.
func (b *Bot) takeDelete(token string) (deleteTarget, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	item, ok := b.deletes[token]
	delete(b.deletes, token)
	if !ok || time.Since(item.created) > 10*time.Minute {
		return deleteTarget{}, false
	}
	return item, true
}

// ── queue ─────────────────────────────────────────────────────────────────────────────────────

// runTask builds one title and reports on its own message.
func (b *Bot) runTask(ctx context.Context, task *task) {
	src, ok := b.registry.ByName(task.source)
	if !ok {
		_ = b.api.EditMessage(ctx, task.chatID, task.messageID, "источник не найден")
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.cancels[task.id] = cancel
	b.mu.Unlock()
	defer func() {
		cancel()
		b.mu.Lock()
		delete(b.cancels, task.id)
		b.busy[task.source] = false
		if task.ended.IsZero() {
			task.ended = time.Now()
			task.status = "failed"
		}
		b.sweep()
		b.mu.Unlock()
		go b.dispatch(ctx)
	}()

	edit := func(text string) {
		_ = b.api.EditMessage(ctx, task.chatID, task.messageID, text)
	}

	logx.Infof("задача: %s", task.slug)
	title, err := src.Title(ctx, task.slug)
	if err != nil {
		logx.Errorf("задача %s: %v", task.slug, err)
		edit("не нашёл: " + html.EscapeString(err.Error()))
		return
	}
	chapters, err := src.Chapters(ctx, title.Slug)
	if err != nil {
		edit("ошибка: " + html.EscapeString(err.Error()))
		return
	}
	volumes, err := build.PlanVolumes(src.Kind(), chapters)
	if err != nil {
		edit(html.EscapeString(err.Error()))
		return
	}

	selected, err := selectVolumes(volumes, task.volumes, task.all)
	if err != nil {
		edit(html.EscapeString(err.Error()))
		return
	}

	// The queue shows progress per title, so its counters start with the plan.
	chaptersInPlan := 0
	for _, volume := range selected {
		chaptersInPlan += len(volume.Chapters)
	}
	b.mu.Lock()
	task.title = title.Title
	task.volTotal = len(selected)
	task.chTotal = chaptersInPlan
	b.mu.Unlock()

	titleID, err := b.st.UpsertTitle(ctx, store.Title{
		Source:        src.Name(),
		Slug:          title.Slug,
		Title:         title.Title,
		Authors:       title.Authors,
		CoverURL:      title.CoverURL,
		SourceURL:     title.SourceURL,
		TotalChapters: len(chapters),
		TotalVolumes:  len(volumes),
		Kind:          string(src.Kind()),
		Status:        title.Status,
	})
	if err != nil {
		edit("ошибка базы: " + html.EscapeString(err.Error()))
		return
	}

	covers := map[string]string{}
	if coversSource, ok := src.(source.CoverSource); ok {
		if found, err := coversSource.VolumeCovers(ctx, title.Slug); err == nil {
			covers = found
		}
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
		Source:        src,
		Store:         b.st,
		Title:         title,
		TitleID:       titleID,
		Volumes:       selected,
		Covers:        covers,
		Dock:          b.cfg.BookDock,
		IncludeImages: b.cfg.ImagesEnabled(),
		WaitForDock:   false,
		Progress:      progress,
		OnVolume: func(done, total int, result pipeline.Result) {
			b.mu.Lock()
			task.volDone = done
			// Chapters of this volume were already counted one by one while it was built.
			task.chDone = task.chBase + result.Chapters
			task.chBase = task.chDone
			b.mu.Unlock()
		},
		OnChapter: func(done, total int) {
			b.mu.Lock()
			task.chDone = task.chBase + done
			b.mu.Unlock()
		},
	})

	var out strings.Builder
	fmt.Fprintf(&out, "<b>%s</b>\n", html.EscapeString(title.Title))
	for _, result := range results {
		fmt.Fprintf(&out, "%s: %d глав, %s\n", html.EscapeString(result.Label), result.Chapters, humanSize(result.Size))
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
	b.mu.Lock()
	task.ended = time.Now()
	if err != nil {
		task.status = "failed"
		task.err = err.Error()
	} else {
		task.status = "done"
		task.volDone = len(results)
	}
	b.mu.Unlock()
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
