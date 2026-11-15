package build

import (
	"testing"

	"github.com/Mimic890/yomibako/internal/source"
)

func chapter(volume, number string, index float64) source.Chapter {
	return source.Chapter{Volume: volume, Number: number, Index: index, DisplayTitle: "Том " + volume + " Глава " + number}
}

func TestGroupVolumes(t *testing.T) {
	chapters := []source.Chapter{
		chapter("2", "1", 3),
		chapter("1", "1", 1),
		chapter("1", "2", 2),
		chapter("", "1", 4),
	}

	volumes := GroupVolumes(chapters)
	if len(volumes) != 3 {
		t.Fatalf("expected 3 volumes, got %d", len(volumes))
	}
	if volumes[0].Label != "0" || len(volumes[0].Chapters) != 1 {
		t.Errorf("volume without a label = %+v", volumes[0])
	}
	if volumes[1].Label != "1" || len(volumes[1].Chapters) != 2 {
		t.Errorf("volume 1 = %+v", volumes[1])
	}
	if volumes[2].Label != "2" {
		t.Errorf("volume 2 = %+v", volumes[2])
	}
}

func TestGroupVolumesSortsNumerically(t *testing.T) {
	chapters := []source.Chapter{chapter("10", "1", 10), chapter("2", "1", 2), chapter("1", "1", 1)}
	volumes := GroupVolumes(chapters)
	want := []string{"1", "2", "10"}
	for i, label := range want {
		if volumes[i].Label != label {
			t.Fatalf("volume %d = %q, want %q", i, volumes[i].Label, label)
		}
	}
}

func TestVolumeTitle(t *testing.T) {
	cases := map[string]string{
		"1":  "Том 1",
		"12": "Том 12",
		"":   "Без тома",
		"0":  "Без тома",
	}
	for label, want := range cases {
		if got := VolumeTitle(label); got != want {
			t.Errorf("VolumeTitle(%q) = %q, want %q", label, got, want)
		}
	}
}
