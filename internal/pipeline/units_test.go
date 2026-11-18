package pipeline

import (
	"testing"

	"github.com/Mimic890/yomibako/internal/build"
	"github.com/Mimic890/yomibako/internal/source"
)

func testVolumes() []build.Volume {
	return []build.Volume{
		{Label: "1", Number: 1, Chapters: []source.Chapter{
			{DisplayTitle: "Том 1 Глава 1"},
			{DisplayTitle: "Том 1 Глава 2"},
		}},
		{Label: "2", Number: 2, Chapters: []source.Chapter{
			{DisplayTitle: "Том 2 Глава 3"},
		}},
	}
}

func TestUnitsSingleFile(t *testing.T) {
	opts := Options{Title: source.Title{Title: "Икс"}, Volumes: testVolumes(), Layout: build.LayoutSingle}
	units := units(opts)
	if len(units) != 1 {
		t.Fatalf("expected one unit, got %d", len(units))
	}
	if len(units[0].Chapters) != 3 {
		t.Fatalf("expected every chapter in the single file, got %d", len(units[0].Chapters))
	}
	if name := unitName(opts, units[0]); name != "Икс" {
		t.Fatalf("unexpected file name: %q", name)
	}
}

func TestUnitsPerChapter(t *testing.T) {
	opts := Options{Title: source.Title{Title: "Икс"}, Volumes: testVolumes(), Layout: build.LayoutChapters}
	units := units(opts)
	if len(units) != 3 {
		t.Fatalf("expected one unit per chapter, got %d", len(units))
	}
	labels := map[string]bool{}
	for _, unit := range units {
		if len(unit.Chapters) != 1 {
			t.Fatalf("unit %q holds %d chapters", unit.Label, len(unit.Chapters))
		}
		labels[unit.Label] = true
	}
	if len(labels) != 3 {
		t.Fatalf("chapter units share a label, the database would merge them: %v", labels)
	}
}

func TestUnitsPerVolume(t *testing.T) {
	opts := Options{Title: source.Title{Title: "Икс"}, Volumes: testVolumes()}
	units := units(opts)
	if len(units) != 2 {
		t.Fatalf("expected one unit per volume, got %d", len(units))
	}
	if name := unitName(opts, units[0]); name != "Икс - Том 1" {
		t.Fatalf("unexpected file name: %q", name)
	}
}
