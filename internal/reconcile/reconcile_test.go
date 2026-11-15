package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mimic890/yomibako/internal/epub"
	"github.com/Mimic890/yomibako/internal/store"
)

func writeBook(t *testing.T, dir, identifier string) {
	t.Helper()
	data, err := epub.Build(epub.Book{
		Metadata: epub.Metadata{Title: "Книга", Identifier: identifier},
		Chapters: []epub.Chapter{{Title: "Глава 1", HTML: "<p>текст</p>"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "book.epub"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunMarksImportedAndMissing(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	id, err := st.UpsertTitle(ctx, store.Title{Source: "ranobelib", Slug: "1--x", Title: "Книга"})
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"1", "2"} {
		if err := st.RecordVolume(ctx, store.Volume{TitleID: id, Label: label, Status: store.StatusBuilt}); err != nil {
			t.Fatal(err)
		}
	}

	root := t.TempDir()
	writeBook(t, filepath.Join(root, "Автор", "Книга", "01"), "urn:ranobelib:1--x:1")

	report, err := Run(ctx, st, root)
	if err != nil {
		t.Fatal(err)
	}
	if report.Imported != 1 || report.Missing != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}

	titles, err := st.Titles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, volume := range titles[0].Volumes {
		statuses[volume.Label] = volume.Status
	}
	if statuses["1"] != store.StatusImported || statuses["2"] != store.StatusMissing {
		t.Fatalf("unexpected statuses: %+v", statuses)
	}
}

func TestRunRequiresDirectory(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := Run(context.Background(), st, ""); err == nil {
		t.Fatal("expected an error without a library directory")
	}
}
