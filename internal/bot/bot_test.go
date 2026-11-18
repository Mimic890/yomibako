package bot

import (
	"errors"
	"strings"
	"testing"

	"github.com/Mimic890/yomibako/internal/build"
	"github.com/Mimic890/yomibako/internal/config"
	"github.com/Mimic890/yomibako/internal/sources"
)

func volumes() []build.Volume {
	return []build.Volume{
		{Label: "1", Number: 1},
		{Label: "2", Number: 2},
		{Label: "3", Number: 3},
	}
}

func TestSelectVolumes(t *testing.T) {
	all := volumes()

	first, err := selectVolumes(all, "", false)
	if err != nil || len(first) != 1 || first[0].Label != "1" {
		t.Fatalf("default selection = %+v, %v", first, err)
	}

	every, err := selectVolumes(all, "", true)
	if err != nil || len(every) != 3 {
		t.Fatalf("all selection = %+v, %v", every, err)
	}

	some, err := selectVolumes(all, "1,3", false)
	if err != nil || len(some) != 2 || some[1].Label != "3" {
		t.Fatalf("list selection = %+v, %v", some, err)
	}

	if _, err := selectVolumes(all, "9", false); err == nil {
		t.Fatal("expected an error for a missing volume")
	}
}

func TestResolve(t *testing.T) {
	registry := sources.New(config.Default())

	cases := []struct {
		input  string
		source string
		slug   string
	}{
		{"https://ranobelib.me/ru/book/20818--lord-of-the-mysteries", "ranobelib", "20818--lord-of-the-mysteries"},
		{"https://mangalib.me/ru/manga/4464--jujutsu-kaisen", "mangalib", "4464--jujutsu-kaisen"},
		{"https://ranobehub.org/ranobe/1034-bleach-cant-fear-your-own-world", "ranobehub", "1034-bleach-cant-fear-your-own-world"},
		{"https://tl.rulate.ru/book/102204", "rulate", "102204"},
		{"https://tl.rulate.ru/book/102204/ya-prevratil", "rulate", "102204"},
	}
	for _, tc := range cases {
		src, slug, err := registry.Resolve(tc.input)
		if err != nil {
			t.Errorf("Resolve(%q): %v", tc.input, err)
			continue
		}
		if src.Name() != tc.source || slug != tc.slug {
			t.Errorf("Resolve(%q) = %s, %q; want %s, %q", tc.input, src.Name(), slug, tc.source, tc.slug)
		}
	}

	if _, _, err := registry.Resolve("lord-of-the-mysteries"); !errors.Is(err, sources.ErrNotURL) {
		t.Errorf("a bare slug must be refused, got %v", err)
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int]string{
		512:     "512 Б",
		2048:    "2.0 КБ",
		5 << 20: "5.0 МБ",
	}
	for size, want := range cases {
		if got := humanSize(size); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", size, got, want)
		}
	}
}

func TestVolumeButtons(t *testing.T) {
	volumes := []build.Volume{
		{Label: "1", Number: 1},
		{Label: "2", Number: 2},
		{Label: "3", Number: 3},
		{Label: "4", Number: 4},
		{Label: "5", Number: 5},
	}
	bot := &Bot{picks: map[string]pick{}}
	rows := bot.volumeButtons("ranobelib", "1--x", volumes, map[string]string{"2": "imported"}, false)

	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if len(rows[0]) != 4 || len(rows[1]) != 1 {
		t.Fatalf("unexpected layout: %+v", rows)
	}
	if rows[0][1].Text != "Том 2 ✓" {
		t.Errorf("built volume is not marked: %q", rows[0][1].Text)
	}
	// Telegram rejects callback data over 64 bytes, so the slug travels as a token.
	if len(rows[0][0].Data) > 64 {
		t.Errorf("callback data is too long: %q", rows[0][0].Data)
	}
	parts := strings.Split(rows[0][0].Data, ":")
	if len(parts) != 4 || parts[0] != "v" || parts[1] != "ranobelib" || parts[3] != "1" {
		t.Errorf("unexpected callback data: %q", rows[0][0].Data)
	}
	if item, ok := bot.peekPick(parts[2]); !ok || item.slug != "1--x" {
		t.Errorf("token does not lead back to the slug: %q", parts[2])
	}
	if rows[2][0].Data != "a:ranobelib:"+parts[2] {
		t.Errorf("unexpected all button: %+v", rows[2][0])
	}
}

func TestHelpListsSources(t *testing.T) {
	cfg := config.Default()
	b := New(cfg, sources.New(cfg), nil)

	text := b.help()
	for _, want := range []string{"RanobeLib", "RanobeHub", "TL.Rulate", "MangaLib", "HentaiLib"} {
		if !strings.Contains(text, want) {
			t.Errorf("помощь не упоминает %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "/search") {
		t.Error("поиск больше не должен предлагаться в помощи")
	}
	if !strings.Contains(text, "Источники ранобэ") || !strings.Contains(text, "Источники манги и манхвы") {
		t.Errorf("помощь не делит источники на ранобэ и мангу:\n%s", text)
	}
}

func TestQueueText(t *testing.T) {
	bot := &Bot{registry: nil, tasks: []*task{
		{source: "mangalib", title: "Хоримия", status: "running", volDone: 3, volTotal: 17, chDone: 28, chTotal: 150},
		{source: "mangalib", slug: "4464--jujutsu-kaisen", status: "queued"},
		{source: "ranobelib", title: "Слизь", status: "done", volDone: 2, chDone: 40},
	}}
	text := bot.queueText()
	for _, want := range []string{"Хоримия", "3/17 томов", "28/150 глав", "в очереди", "готово: 2 томов"} {
		if !strings.Contains(text, want) {
			t.Errorf("queue text has no %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "Хоримия") > strings.Index(text, "Слизь") {
		t.Errorf("sources are mixed up:\n%s", text)
	}
}

func TestEmptyQueueText(t *testing.T) {
	bot := &Bot{}
	if got := bot.queueText(); got != "очередь пуста" {
		t.Errorf("unexpected text: %q", got)
	}
}

func TestSplitMessage(t *testing.T) {
	short := "коротко"
	if parts := splitMessage(short, 100); len(parts) != 1 || parts[0] != short {
		t.Errorf("short text was cut: %v", parts)
	}

	long := strings.Repeat("строка\n", 1000)
	parts := splitMessage(long, 4000)
	if len(parts) < 2 {
		t.Fatalf("long text was not split: %d parts", len(parts))
	}
	for _, part := range parts {
		if len(part) > 4000 {
			t.Errorf("part is %d bytes", len(part))
		}
	}
	if joined := strings.Join(parts, ""); len(joined) < len(long)/2 {
		t.Errorf("text was lost: %d of %d", len(joined), len(long))
	}
}

func TestNextTasks(t *testing.T) {
	tasks := []*task{
		{source: "mangalib", status: "queued"},
		{source: "mangalib", status: "queued"},
		{source: "ranobelib", status: "queued"},
		{source: "comx", status: "done"},
	}
	busy := map[string]bool{}
	start := nextTasks(tasks, busy)
	if len(start) != 2 {
		t.Fatalf("expected one task per source, got %d", len(start))
	}
	if start[0].source != "mangalib" || start[1].source != "ranobelib" {
		t.Errorf("wrong tasks started: %s, %s", start[0].source, start[1].source)
	}
	if !busy["mangalib"] || !busy["ranobelib"] {
		t.Errorf("sources are not marked busy: %v", busy)
	}

	// The second mangalib task waits until the first one is finished.
	if again := nextTasks(tasks, busy); len(again) != 0 {
		t.Errorf("a second task of a busy source started: %d", len(again))
	}
	busy["mangalib"] = false
	if again := nextTasks(tasks, busy); len(again) != 1 || again[0].source != "mangalib" {
		t.Errorf("the queued task did not start after the source went idle: %v", again)
	}
}
