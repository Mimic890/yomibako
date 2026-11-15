package bot

import (
	"testing"

	"github.com/Mimic890/yomibako/internal/build"
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

func TestParseSlug(t *testing.T) {
	cases := map[string]string{
		"lord-of-the-mysteries": "lord-of-the-mysteries",
		"https://ranobelib.me/ru/book/20818--lord-of-the-mysteries":           "20818--lord-of-the-mysteries",
		"https://ranobelib.me/ru/book/6777--no-game-no-life-novel?ui=9594536": "6777--no-game-no-life-novel",
	}
	for input, want := range cases {
		if got := parseSlug(input); got != want {
			t.Errorf("parseSlug(%q) = %q, want %q", input, got, want)
		}
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
