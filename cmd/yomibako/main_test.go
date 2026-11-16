package main

import (
	"testing"

	"github.com/Mimic890/yomibako/internal/pipeline"
)

func TestParseSlug(t *testing.T) {
	cases := map[string]string{
		"lord-of-the-mysteries": "lord-of-the-mysteries",
		"https://ranobelib.me/ru/book/20818--lord-of-the-mysteries":           "20818--lord-of-the-mysteries",
		"https://ranobelib.me/ru/book/6777--no-game-no-life-novel?ui=9594536": "6777--no-game-no-life-novel",
		"ranobelib.me/ru/book/1--x/":                                          "1--x",
	}
	for input, want := range cases {
		if got := parseSlug(input); got != want {
			t.Errorf("parseSlug(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDockFileName(t *testing.T) {
	if got := pipeline.DockFileName("Повелитель тайн", "Том 01"); got != "Повелитель тайн - Том 01" {
		t.Errorf("DockFileName = %q", got)
	}
	if got := pipeline.DockFileName("Книга/с: слэшем", "Без тома"); got != "Книга-с- слэшем - Без тома" {
		t.Errorf("DockFileName with unsafe characters = %q", got)
	}
}

func TestSafeName(t *testing.T) {
	if got := pipeline.SafeName("  a/b:c  "); got != "a-b-c" {
		t.Errorf("safeName = %q", got)
	}
	if got := pipeline.SafeName("   "); got != "book" {
		t.Errorf("safeName of blank = %q", got)
	}
}
