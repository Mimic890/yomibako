package logx

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestLevelsFilter(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	defer SetOutput(io.Discard)

	if err := SetLevel("warn"); err != nil {
		t.Fatal(err)
	}
	Infof("скрыто")
	Warnf("видно")
	Errorf("тоже видно")

	out := buf.String()
	if strings.Contains(out, "скрыто") {
		t.Errorf("info line leaked at warn level: %q", out)
	}
	if !strings.Contains(out, "[WARN] видно") || !strings.Contains(out, "[ERROR] тоже видно") {
		t.Errorf("expected warn and error lines, got %q", out)
	}
}

func TestParseLevel(t *testing.T) {
	for name, want := range map[string]Level{"debug": Debug, "info": Info, "warn": Warn, "error": Error, "": Info} {
		got, err := ParseLevel(name)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v", name, got, err)
		}
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Error("expected an error for an unknown level")
	}
}
