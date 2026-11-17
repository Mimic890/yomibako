package cookies

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func line(domain, name, value string) string {
	return strings.Join([]string{domain, "TRUE", "/", "FALSE", "0", name, value}, "\t")
}

func TestParseAndFor(t *testing.T) {
	jar, err := Parse([]byte("# Netscape HTTP Cookie File\n" + line(".ranobelib.me", "token", "abc") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := jar.For("ranobelib.me"); got != "token=abc" {
		t.Errorf("For(ranobelib.me) = %q", got)
	}
	if got := jar.For("api.ranobelib.me"); got != "token=abc" {
		t.Errorf("a subdomain should inherit the parent's header, got %q", got)
	}
	if got := jar.For("example.org"); got != "" {
		t.Errorf("an unrelated host got %q", got)
	}
	if _, err := Parse([]byte("не куки\n")); err == nil {
		t.Error("a file without cookie lines must be rejected")
	}
}

func TestMergeKeepsOtherDomains(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	existing := line(".ranobelib.me", "token", "old") + "\n" + line(".hentailib.me", "session", "h") + "\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	jar, err := Merge(path, []byte(line(".com-x.life", "__guard_trust", "yes")+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	domains := strings.Join(jar.Domains(), ",")
	if domains != "com-x.life,hentailib.me,ranobelib.me" {
		t.Fatalf("domains = %s", domains)
	}

	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "__guard_trust") || !strings.Contains(string(body), "session") {
		t.Errorf("merged file lost a line:\n%s", body)
	}
}

func TestMergeReplacesDomainWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte(line(".com-x.life", "old", "stale")+"\n"+line(".ranobelib.me", "keep", "1")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Merge(path, []byte(line(".com-x.life", "fresh", "2")+"\n")); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), "stale") {
		t.Errorf("the old cookie survived the merge:\n%s", body)
	}
	if !strings.Contains(string(body), "keep") {
		t.Errorf("an untouched domain was dropped:\n%s", body)
	}
}
