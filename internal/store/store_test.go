package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestUpsertTitleIsIdempotent(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	first, err := st.UpsertTitle(ctx, Title{Source: "ranobelib", Slug: "1--x", Title: "Икс", Authors: []string{"Автор"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.UpsertTitle(ctx, Title{Source: "ranobelib", Slug: "1--x", Title: "Икс 2", Authors: []string{"Автор"}})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("ids differ: %d and %d", first, second)
	}

	titles, err := st.Titles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(titles) != 1 || titles[0].Title != "Икс 2" {
		t.Fatalf("unexpected titles: %+v", titles)
	}
	if len(titles[0].Authors) != 1 || titles[0].Authors[0] != "Автор" {
		t.Fatalf("authors lost: %+v", titles[0].Authors)
	}
}

func TestRecordVolumeUpdatesInsteadOfDuplicating(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	id, err := st.UpsertTitle(ctx, Title{Source: "ranobelib", Slug: "1--x", Title: "Икс"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordVolume(ctx, Volume{TitleID: id, Label: "1", Number: 1, Chapters: 5, Status: StatusBuilt, SizeBytes: 100}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordVolume(ctx, Volume{TitleID: id, Label: "1", Number: 1, Chapters: 6, Status: StatusImported, SizeBytes: 200}); err != nil {
		t.Fatal(err)
	}

	titles, err := st.Titles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(titles[0].Volumes) != 1 {
		t.Fatalf("expected one volume, got %d", len(titles[0].Volumes))
	}
	volume := titles[0].Volumes[0]
	if volume.Chapters != 6 || volume.Status != StatusImported || volume.SizeBytes != 200 {
		t.Fatalf("volume was not updated: %+v", volume)
	}
}

func TestVolumeLabels(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	id, _ := st.UpsertTitle(ctx, Title{Source: "ranobelib", Slug: "1--x", Title: "Икс"})
	_ = st.RecordVolume(ctx, Volume{TitleID: id, Label: "1", Number: 1, Status: StatusBuilt})
	_ = st.RecordVolume(ctx, Volume{TitleID: id, Label: "2", Number: 2, Status: StatusImported})

	labels, err := st.VolumeLabels(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 2 || labels["2"] != StatusImported {
		t.Fatalf("unexpected labels: %+v", labels)
	}
}

func TestSubscriptionLifecycle(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	id, _ := st.UpsertTitle(ctx, Title{Source: "ranobelib", Slug: "1--x", Title: "Икс"})

	if err := st.Subscribe(ctx, id, 12); err != nil {
		t.Fatal(err)
	}
	subs, err := st.Subscriptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Subscription.IntervalHours != 12 {
		t.Fatalf("unexpected subscriptions: %+v", subs)
	}

	if err := st.MarkChecked(ctx, id, "3"); err != nil {
		t.Fatal(err)
	}
	subs, _ = st.Subscriptions(ctx)
	if subs[0].Subscription.LastVolumeLabel != "3" || subs[0].Subscription.LastCheckedAt == nil {
		t.Fatalf("check was not recorded: %+v", subs[0].Subscription)
	}

	if err := st.Unsubscribe(ctx, id); err != nil {
		t.Fatal(err)
	}
	subs, _ = st.Subscriptions(ctx)
	if len(subs) != 0 {
		t.Fatalf("subscription survived: %+v", subs)
	}
}

func TestStats(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	id, _ := st.UpsertTitle(ctx, Title{Source: "ranobelib", Slug: "1--x", Title: "Икс"})
	_ = st.RecordVolume(ctx, Volume{TitleID: id, Label: "1", Number: 1, Status: StatusImported})
	_ = st.Subscribe(ctx, id, 24)

	stats, err := st.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Titles != 1 || stats.Volumes != 1 || stats.Imported != 1 || stats.Subscriptions != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestMarkVolumeImported(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	id, _ := st.UpsertTitle(ctx, Title{Source: "ranobelib", Slug: "1--x", Title: "Икс"})
	_ = st.RecordVolume(ctx, Volume{TitleID: id, Label: "1", Number: 1, Status: StatusBuilt})

	if err := st.MarkVolumeImported(ctx, id, "1"); err != nil {
		t.Fatal(err)
	}
	titles, _ := st.Titles(ctx)
	volume := titles[0].Volumes[0]
	if volume.Status != StatusImported || volume.ImportedAt == nil {
		t.Fatalf("volume not marked imported: %+v", volume)
	}
	if time.Since(*volume.ImportedAt) > time.Minute {
		t.Fatalf("imported_at looks wrong: %v", volume.ImportedAt)
	}
}

func TestFindTitle(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	if _, err := st.UpsertTitle(ctx, Title{Source: "ranobelib", Slug: "1--x", Title: "Икс"}); err != nil {
		t.Fatal(err)
	}

	found, err := st.FindTitle(ctx, "ranobelib", "1--x")
	if err != nil {
		t.Fatal(err)
	}
	if found == nil || found.Title != "Икс" {
		t.Fatalf("unexpected title: %+v", found)
	}

	missing, err := st.FindTitle(ctx, "ranobelib", "nope")
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Fatalf("expected nil for a missing title, got %+v", missing)
	}
}
