package cbz

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/Mimic890/yomibako/internal/source"
)

func readArchive(t *testing.T, data []byte) map[string]string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := make(map[string]string, len(reader.File))
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(handle)
		handle.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[file.Name] = string(content)
	}
	return out
}

func TestWriteNamesPagesInOrder(t *testing.T) {
	pages := []source.Image{
		{MediaType: "image/jpeg", Bytes: []byte{0xff, 0xd8, 0xff}},
		{MediaType: "image/png", Bytes: []byte{0x89, 'P', 'N', 'G'}},
	}
	var buf bytes.Buffer
	if err := Write(&buf, pages, Meta{Series: "Магическая битва", Number: 1}); err != nil {
		t.Fatal(err)
	}

	files := readArchive(t, buf.Bytes())
	if _, ok := files["001.jpg"]; !ok {
		t.Errorf("first page is not 001.jpg: %v", keys(files))
	}
	if _, ok := files["002.png"]; !ok {
		t.Errorf("second page is not 002.png: %v", keys(files))
	}
}

func TestWriteComicInfo(t *testing.T) {
	var buf bytes.Buffer
	err := Write(&buf, []source.Image{{MediaType: "image/jpeg", Bytes: []byte{0xff, 0xd8, 0xff}}}, Meta{
		Title:    "Глава 1",
		Series:   "Магическая битва",
		Number:   1,
		Volume:   1,
		Summary:  "Описание",
		Writer:   []string{"Автор"},
		Genre:    []string{"Экшен"},
		Language: "ru",
		Manga:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	info := readArchive(t, buf.Bytes())["ComicInfo.xml"]
	for _, want := range []string{
		"<Series>Магическая битва</Series>",
		"<Number>1</Number>",
		"<Volume>1</Volume>",
		"<PageCount>1</PageCount>",
		"<LanguageISO>ru</LanguageISO>",
		"<Manga>YesAndRightToLeft</Manga>",
	} {
		if !strings.Contains(info, want) {
			t.Errorf("ComicInfo is missing %q:\n%s", want, info)
		}
	}
	// ComicInfo is an xs:sequence: Series must come before Number, Number before Volume.
	if strings.Index(info, "<Series>") > strings.Index(info, "<Number>") {
		t.Errorf("elements are out of order:\n%s", info)
	}
}

func TestWriteEscapesMetadata(t *testing.T) {
	var buf bytes.Buffer
	err := Write(&buf, []source.Image{{MediaType: "image/jpeg", Bytes: []byte{0xff, 0xd8, 0xff}}}, Meta{
		Series: `Том <1> & "два"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	info := readArchive(t, buf.Bytes())["ComicInfo.xml"]
	if strings.Contains(info, "<1>") {
		t.Fatalf("series was not escaped:\n%s", info)
	}
}

func TestWriteRejectsEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, nil, Meta{}); err == nil {
		t.Fatal("expected an error for a chapter without pages")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}
