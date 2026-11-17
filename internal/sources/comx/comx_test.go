package comx

import "testing"

func TestNormalizeSlug(t *testing.T) {
	cases := map[string]string{
		"https://com-x.life/11082-chelovek-benzopila-2-2026.html":          "11082-chelovek-benzopila-2-2026",
		"https://com-x.life/11082-chelovek-benzopila-2-2026.html#chapters": "11082-chelovek-benzopila-2-2026",
		"https://ru.com-x.life/11082-chelovek-benzopila-2-2026.html":       "11082-chelovek-benzopila-2-2026",
		"https://com-x.life/reader/11082/556677":                           "11082",
		"11082-chelovek-benzopila-2-2026":                                  "11082-chelovek-benzopila-2-2026",
	}
	for input, want := range cases {
		if got := normalizeSlug(input); got != want {
			t.Errorf("normalizeSlug(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExtractData(t *testing.T) {
	page := []byte(`<html><script>window.__DATA__ = {"news_id":11082,"chapters":[{"id":556677,"title":"Глава 1","number":1,"date":"2024-01-01"}],"host":"img.com-x.life","host_ru":"rus.com-x.life","images":[]};window.other=1</script></html>`)

	data, err := extractData(page)
	if err != nil {
		t.Fatalf("extractData: %v", err)
	}
	if data.NewsID != 11082 || len(data.Chapters) != 1 || data.Chapters[0].ID != 556677 {
		t.Fatalf("unexpected data: %+v", data)
	}
	if data.Host != "img.com-x.life" {
		t.Errorf("host = %q", data.Host)
	}
}

func TestIsChallenge(t *testing.T) {
	if !isChallenge([]byte(`<html><script>var s="pow_nonce";</script></html>`)) {
		t.Error("challenge page not recognised")
	}
	if isChallenge([]byte(`<html><script>window.__DATA__ = {"news_id":1}</script></html>`)) {
		t.Error("a real page was taken for a challenge")
	}
}
