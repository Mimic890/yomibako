package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"
)

func sampleBook() Book {
	return Book{
		Metadata: Metadata{
			Title:       "Повелитель тайн",
			Series:      "Повелитель тайн",
			SeriesIndex: 3,
			Authors:     []string{"Ai Qianshui de Wuzei"},
			Description: "Описание",
			Genres:      []string{"Фэнтези"},
			Tags:        []string{"Магия"},
			SourceURL:   "https://ranobelib.me/ru/book/1--slug",
			Identifier:  "urn:ranobelib:1--slug:3",
			Language:    "ru",
			ReleaseDate: "2018 г.",
		},
		Chapters: []Chapter{
			{Title: "Глава 1", HTML: "<p>Первый</p>"},
			{Title: "Глава 2", HTML: "<p>Второй</p>"},
		},
		Cover:    &Image{MediaType: "image/jpeg", Bytes: []byte{0xff, 0xd8, 0xff, 0xe0, 0x00}},
		Images:   []Image{{FileName: "img-1.png", MediaType: "image/png", Bytes: []byte{0x89, 'P', 'N', 'G'}}},
		Modified: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func readZip(t *testing.T, data []byte) *zip.Reader {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("archive is not a valid zip: %v", err)
	}
	return reader
}

func readFile(t *testing.T, reader *zip.Reader, name string) string {
	t.Helper()
	for _, file := range reader.File {
		if file.Name != name {
			continue
		}
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer handle.Close()
		data, err := io.ReadAll(handle)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	t.Fatalf("entry %q not found", name)
	return ""
}

func TestBuildWritesMimetypeFirstAndStored(t *testing.T) {
	data, err := Build(sampleBook())
	if err != nil {
		t.Fatal(err)
	}
	reader := readZip(t, data)
	first := reader.File[0]
	if first.Name != "mimetype" {
		t.Fatalf("first entry is %q, want mimetype", first.Name)
	}
	if first.Method != zip.Store {
		t.Fatalf("mimetype is compressed (method %d)", first.Method)
	}
	if len(first.Extra) != 0 {
		t.Fatalf("mimetype carries a %d-byte extra field; EPUB forbids it", len(first.Extra))
	}
	if got := readFile(t, reader, "mimetype"); got != "application/epub+zip" {
		t.Fatalf("mimetype = %q", got)
	}
}

func TestBuildWritesSeriesMetadata(t *testing.T) {
	data, err := Build(sampleBook())
	if err != nil {
		t.Fatal(err)
	}
	opf := readFile(t, readZip(t, data), "OEBPS/content.opf")
	for _, want := range []string{
		`<meta name="calibre:series" content="Повелитель тайн"/>`,
		`<meta name="calibre:series_index" content="3"/>`,
		`property="belongs-to-collection"`,
		`<dc:date>2018</dc:date>`,
		`<dc:title>Повелитель тайн</dc:title>`,
	} {
		if !strings.Contains(opf, want) {
			t.Errorf("OPF does not contain %q", want)
		}
	}
}

func TestBuildProducesWellFormedXML(t *testing.T) {
	data, err := Build(sampleBook())
	if err != nil {
		t.Fatal(err)
	}
	reader := readZip(t, data)
	for _, file := range reader.File {
		if !strings.HasSuffix(file.Name, ".xml") && !strings.HasSuffix(file.Name, ".opf") &&
			!strings.HasSuffix(file.Name, ".xhtml") && !strings.HasSuffix(file.Name, ".ncx") {
			continue
		}
		content := readFile(t, reader, file.Name)
		decoder := xml.NewDecoder(strings.NewReader(content))
		for {
			if _, err := decoder.Token(); err != nil {
				if err == io.EOF {
					break
				}
				t.Fatalf("%s is not well-formed XML: %v", file.Name, err)
			}
		}
	}
}

func TestBuildIncludesChaptersAndImages(t *testing.T) {
	data, err := Build(sampleBook())
	if err != nil {
		t.Fatal(err)
	}
	reader := readZip(t, data)
	names := make(map[string]bool, len(reader.File))
	for _, file := range reader.File {
		names[file.Name] = true
	}
	for _, want := range []string{
		"OEBPS/text/chapter-001.xhtml",
		"OEBPS/text/chapter-002.xhtml",
		"OEBPS/text/cover.xhtml",
		"OEBPS/text/title.xhtml",
		"OEBPS/images/cover.jpg",
		"OEBPS/images/img-1.png",
		"OEBPS/nav.xhtml",
		"OEBPS/toc.ncx",
	} {
		if !names[want] {
			t.Errorf("missing entry %q", want)
		}
	}
}

func TestBuildRejectsEmptyBook(t *testing.T) {
	if _, err := Build(Book{Metadata: Metadata{Title: "x"}}); err == nil {
		t.Fatal("expected an error for a book without chapters")
	}
	if _, err := Build(Book{Chapters: []Chapter{{Title: "x"}}}); err == nil {
		t.Fatal("expected an error for a book without a title")
	}
}

func TestBuildEscapesMetadata(t *testing.T) {
	book := sampleBook()
	book.Metadata.Title = `Том <1> & "два"`
	data, err := Build(book)
	if err != nil {
		t.Fatal(err)
	}
	opf := readFile(t, readZip(t, data), "OEBPS/content.opf")
	if strings.Contains(opf, "<1>") {
		t.Fatalf("title was not escaped: %s", opf)
	}
	if !strings.Contains(opf, "&lt;1&gt;") {
		t.Fatalf("expected escaped title in OPF")
	}
}
