package ranobelib

import "testing"

func TestSearchName(t *testing.T) {
	cases := map[string]string{
		"6777--no-game-no-life-novel":  "no-game-no-life-novel",
		"lord-of-the-mysteries":        "lord-of-the-mysteries",
		"20818--lord-of-the-mysteries": "lord-of-the-mysteries",
		"12--34--x":                    "34--x",
	}
	for slug, want := range cases {
		if got := searchName(slug); got != want {
			t.Errorf("searchName(%q) = %q, want %q", slug, got, want)
		}
	}
}
