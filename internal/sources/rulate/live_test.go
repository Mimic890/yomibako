package rulate

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveChapter hits the real site. It only runs with YOMIBAKO_LIVE=1 so the normal test run
// stays offline.
func TestLiveChapter(t *testing.T) {
	if os.Getenv("YOMIBAKO_LIVE") == "" {
		t.Skip("set YOMIBAKO_LIVE=1 to hit tl.rulate.ru")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	src := New()
	title, err := src.Title(ctx, "https://tl.rulate.ru/book/102204")
	if err != nil {
		t.Fatalf("title: %v", err)
	}
	if title.Title == "" || title.CoverURL == "" {
		t.Fatalf("incomplete card: %+v", title)
	}

	chapters, err := src.Chapters(ctx, title.Slug)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v (%d)", err, len(chapters))
	}
	if chapters[0].Ref == "" {
		t.Fatal("chapter has no ref")
	}

	content, err := src.Chapter(ctx, title.Slug, chapters[0])
	if err != nil {
		t.Fatalf("chapter: %v", err)
	}
	if len(content.Text) < 200 {
		t.Fatalf("chapter text looks empty (%d chars)", len(content.Text))
	}
	t.Logf("%s: %d глав, первая глава %q, %d символов, картинок %d",
		title.Title, len(chapters), content.Title, len(content.Text), len(content.Images))
}
