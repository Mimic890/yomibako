package ranobelib

import "testing"

func TestPickSlug(t *testing.T) {
	slugs := []string{
		"6777--no-game-no-life-novel",
		"101533--no-game-no-life-gaiden-practical-war-game",
		"20818--lord-of-the-mysteries",
	}

	cases := []struct {
		want    string
		slug    string
		wantErr bool
	}{
		{want: "6777--no-game-no-life-novel", slug: "no-game-no-life-novel"},
		{want: "6777--no-game-no-life-novel", slug: "6777--no-game-no-life-novel"},
		{want: "20818--lord-of-the-mysteries", slug: "lord-of-the-mysteries"},
		{want: "6777--no-game-no-life-novel", slug: "no-game-no-life"}, // shortest prefix wins
		{slug: "nothing-here", wantErr: true},
	}
	for _, tc := range cases {
		got, err := pickSlug(slugs, tc.slug)
		if tc.wantErr {
			if err == nil {
				t.Errorf("pickSlug(%q) = %q, expected an error", tc.slug, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("pickSlug(%q): %v", tc.slug, err)
			continue
		}
		if got != tc.want {
			t.Errorf("pickSlug(%q) = %q, want %q", tc.slug, got, tc.want)
		}
	}
}
