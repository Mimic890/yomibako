package bot

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/Mimic890/yomibako/internal/logx"
	"github.com/Mimic890/yomibako/internal/pipeline"
	"github.com/Mimic890/yomibako/internal/source"
	"github.com/Mimic890/yomibako/internal/store"
	"github.com/Mimic890/yomibako/internal/telegram"
)

// chaptersPerPage is how many chapter buttons one message shows.
const chaptersPerPage = 12

// showChapters lists manga chapters as buttons, one page at a time.
func (b *Bot) showChapters(ctx context.Context, chatID int64, src source.Source, slug string, page int) {
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
	if len(chapters) == 0 {
		b.reply(ctx, chatID, "у тайтла нет глав")
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
		Kind:          string(source.KindManga),
	})
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

	pages := (len(chapters) + chaptersPerPage - 1) / chaptersPerPage
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * chaptersPerPage
	end := start + chaptersPerPage
	if end > len(chapters) {
		end = len(chapters)
	}

	rows := chapterButtons(src.Name(), title.Slug, chapters[start:end], known)
	if pages > 1 {
		nav := []telegram.Button{}
		if page > 1 {
			nav = append(nav, telegram.Button{Text: "◀", Data: fmt.Sprintf("p:%s:%s:%d", src.Name(), title.Slug, page-1)})
		}
		nav = append(nav, telegram.Button{Text: fmt.Sprintf("%d/%d", page, pages), Data: "noop"})
		if page < pages {
			nav = append(nav, telegram.Button{Text: "▶", Data: fmt.Sprintf("p:%s:%s:%d", src.Name(), title.Slug, page+1)})
		}
		rows = append(rows, nav)
	}
	rows = append(rows, []telegram.Button{{Text: "Все главы", Data: "a:" + src.Name() + ":" + title.Slug}})

	text := fmt.Sprintf("<b>%s</b>\nглав: %d\n\nвыбери главу:", html.EscapeString(title.Title), len(chapters))
	if _, err := b.api.SendMessageWithKeyboard(ctx, chatID, text, rows); err != nil {
		logx.Errorf("telegram: %v", err)
	}
}

// chapterButtons lays out chapter buttons, four per row.
func chapterButtons(sourceName, slug string, chapters []source.Chapter, known map[string]string) [][]telegram.Button {
	var rows [][]telegram.Button
	var row []telegram.Button
	for _, chapter := range chapters {
		label := "Гл. " + chapter.Number
		if chapter.Number == "" {
			label = fmt.Sprintf("Гл. %g", chapter.Index)
		}
		if _, ok := known[chapter.Number]; ok {
			label += " ✓"
		}
		row = append(row, telegram.Button{Text: label, Data: "v:" + sourceName + ":" + slug + ":" + chapter.Number})
		if len(row) == 4 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

// confirmChapters shows which chapters will be downloaded and waits for a button press.
func (b *Bot) confirmChapters(ctx context.Context, chatID int64, src source.Source, slug string, numbers []string, all bool) {
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

	titleID, _ := b.st.UpsertTitle(ctx, store.Title{
		Source:        src.Name(),
		Slug:          title.Slug,
		Title:         title.Title,
		Authors:       title.Authors,
		CoverURL:      title.CoverURL,
		SourceURL:     title.SourceURL,
		TotalChapters: len(chapters),
		Kind:          string(source.KindManga),
	})
	present := map[string]bool{}
	if labels, err := b.st.VolumeLabels(ctx, titleID); err == nil {
		for label, status := range labels {
			if status != store.StatusMissing {
				present[label] = true
			}
		}
	}

	var selected []source.Chapter
	if all {
		for _, chapter := range chapters {
			if !present[chapter.Number] {
				selected = append(selected, chapter)
			}
		}
	} else {
		wanted := make(map[string]bool, len(numbers))
		for _, number := range numbers {
			wanted[number] = true
		}
		for _, chapter := range chapters {
			if wanted[chapter.Number] {
				selected = append(selected, chapter)
			}
		}
	}

	if len(selected) == 0 {
		b.reply(ctx, chatID, "все выбранные главы уже в библиотеке")
		return
	}

	var out strings.Builder
	fmt.Fprintf(&out, "<b>%s</b>\n", html.EscapeString(title.Title))
	for i, chapter := range selected {
		if i == 12 {
			fmt.Fprintf(&out, "… и ещё %d глав\n", len(selected)-i)
			break
		}
		fmt.Fprintf(&out, "%s\n", html.EscapeString(pipeline.ChapterLabel(chapter)))
	}
	fmt.Fprintf(&out, "\nвсего глав: %d", len(selected))
	if all && len(present) > 0 {
		fmt.Fprintf(&out, "\nуже в библиотеке, пропускаю: %d", len(present))
	}
	if !all && present[selected[0].Number] {
		out.WriteString("\n\nэта глава уже в библиотеке - собрать заново?")
	}

	labels := make([]string, 0, len(selected))
	for _, chapter := range selected {
		labels = append(labels, chapter.Number)
	}
	token := b.remember(pendingJob{source: src.Name(), slug: title.Slug, chapters: labels, all: all, created: time.Now()})

	rows := [][]telegram.Button{
		{{Text: "Подтвердить", Data: "go:" + token}},
		{{Text: "Назад", Data: "t:" + src.Name() + ":" + title.Slug}},
	}
	if _, err := b.api.SendMessageWithKeyboard(ctx, chatID, out.String(), rows); err != nil {
		logx.Errorf("telegram: %v", err)
	}
}

// runMangaJob builds the confirmed chapters.
func (b *Bot) runMangaJob(ctx context.Context, task job, src source.Source) {
	edit := func(text string) {
		_ = b.api.EditMessage(ctx, task.chatID, task.messageID, text)
	}

	title, err := src.Title(ctx, task.slug)
	if err != nil {
		edit("не нашёл: " + html.EscapeString(err.Error()))
		return
	}
	chapters, err := src.Chapters(ctx, title.Slug)
	if err != nil {
		edit("ошибка: " + html.EscapeString(err.Error()))
		return
	}

	wanted := make(map[string]bool, len(task.chapters))
	for _, number := range task.chapters {
		wanted[number] = true
	}
	var selected []source.Chapter
	for _, chapter := range chapters {
		if wanted[chapter.Number] {
			selected = append(selected, chapter)
		}
	}
	if len(selected) == 0 {
		edit("нечего собирать")
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
		Kind:          string(source.KindManga),
	})
	if err != nil {
		edit("ошибка базы: " + html.EscapeString(err.Error()))
		return
	}

	lastEdit := time.Now()
	progress := func(format string, args ...any) {
		if time.Since(lastEdit) < 3*time.Second {
			return
		}
		lastEdit = time.Now()
		edit(fmt.Sprintf("<b>%s</b>\n%s", html.EscapeString(title.Title), html.EscapeString(fmt.Sprintf(format, args...))))
	}

	logx.Infof("сборка «%s»: глав %d", title.Title, len(selected))
	results, err := pipeline.Run(ctx, pipeline.Options{
		Source:   src,
		Store:    b.st,
		Title:    title,
		TitleID:  titleID,
		Chapters: selected,
		Dock:     b.cfg.BookDock,
		Progress: progress,
	})

	var out strings.Builder
	fmt.Fprintf(&out, "<b>%s</b>\n", html.EscapeString(title.Title))
	for _, result := range results {
		fmt.Fprintf(&out, "%s: %d страниц, %s\n", html.EscapeString(result.Label), result.Pages, humanSize(result.Size))
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
		logx.Infof("сборка «%s»: готово, глав %d", title.Title, len(results))
	}
	edit(out.String())
}
