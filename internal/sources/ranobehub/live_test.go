package ranobehub

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveChapter hits the real site, only with YOMIBAKO_LIVE=1.
func TestLiveChapter(t *testing.T) {
	if os.Getenv("YOMIBAKO_LIVE") == "" {
		t.Skip("set YOMIBAKO_LIVE=1 to hit ranobehub.org")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	src := New()
	title, err := src.Title(ctx, "https://ranobehub.org/ranobe/1034-bleach-cant-fear-your-own-world")
	if err != nil {
		t.Fatalf("title: %v", err)
	}
	chapters, err := src.Chapters(ctx, title.Slug)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v (%d)", err, len(chapters))
	}
	if chapters[0].Volume == "" {
		t.Fatal("chapter has no volume")
	}

	content, err := src.Chapter(ctx, title.Slug, chapters[0])
	if err != nil {
		t.Fatalf("chapter: %v", err)
	}
	if len(content.Text) < 200 {
		t.Fatalf("chapter text looks empty (%d chars)", len(content.Text))
	}
	t.Logf("%s: %d глав, %q, %d символов, картинок %d",
		title.Title, len(chapters), content.Title, len(content.Text), len(content.Images))
}
