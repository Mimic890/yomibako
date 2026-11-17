package comx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/Mimic890/yomibako/internal/source"
)

// Source implements source.MangaSource for com-x.life.
type Source struct {
	Client *Client
}

// New returns a source with the public defaults.
func New() *Source { return &Source{Client: NewClient()} }

// Name implements source.Source.
func (s *Source) Name() string { return "comx" }

// Kind implements source.Source.
func (s *Source) Kind() source.Kind { return source.KindManga }

// SetToken is a no-op: Com-X authorises through cookies only.
func (s *Source) SetToken(string) {}

// SetCookies implements the registry's cookie hook.
func (s *Source) SetCookies(value string) { s.Client.SetCookies(value) }

// Search is not exposed anywhere: titles are picked by the user from a link. It is kept because
// the interface requires it, and it uses the site's own search page.
func (s *Source) Search(ctx context.Context, query string, page int) (source.SearchResult, error) {
	return source.SearchResult{}, fmt.Errorf("com-x: поиск не поддерживается, нужна ссылка на тайтл")
}

// pageData is the JSON the site embeds in a document for its own scripts.
type pageData struct {
	NewsID   int `json:"news_id"`
	Chapters []struct {
		ID     int     `json:"id"`
		Title  string  `json:"title"`
		Volume float64 `json:"volume"`
		Number float64 `json:"number"`
		Date   string  `json:"date"`
	} `json:"chapters"`
	Host   string   `json:"host"`
	HostRu string   `json:"host_ru"`
	Images []string `json:"images"`
}

// Title implements source.Source.
func (s *Source) Title(ctx context.Context, slug string) (source.Title, error) {
	path := pagePath(slug)
	body, err := s.Client.get(ctx, s.Client.Base+"/"+path)
	if err != nil {
		return source.Title{}, err
	}
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return source.Title{}, fmt.Errorf("com-x: не разобрал страницу %s: %w", path, err)
	}

	title := textOf(findClass(document, "page__header", "h1"))
	if title == "" {
		title = textOf(findClass(document, "page__title", ""))
	}
	if title == "" {
		return source.Title{}, fmt.Errorf("com-x: на странице %s нет названия", path)
	}

	cover := ""
	if img := findClass(document, "page__poster", "img"); img != nil {
		cover = imageSrc(img)
		if strings.HasPrefix(cover, "/") {
			cover = s.Client.Base + cover
		}
	}

	description := textOf(findClass(document, "page__text", ""))

	authors := make([]string, 0, 1)
	if author := listValue(document, "Автор"); author != "" {
		authors = append(authors, author)
	}

	return source.Title{
		Slug:        path,
		Title:       title,
		Description: description,
		Authors:     authors,
		CoverURL:    cover,
		SourceURL:   s.Client.Base + "/" + path,
	}, nil
}

// Chapters implements source.Source.
func (s *Source) Chapters(ctx context.Context, slug string) ([]source.Chapter, error) {
	path := pagePath(slug)
	body, err := s.Client.get(ctx, s.Client.Base+"/"+path)
	if err != nil {
		return nil, err
	}
	data, err := extractData(body)
	if err != nil {
		return nil, err
	}
	if len(data.Chapters) == 0 {
		return nil, fmt.Errorf("com-x: у тайтла нет глав (возможно, нужна авторизация)")
	}

	chapters := make([]source.Chapter, 0, len(data.Chapters))
	for _, entry := range data.Chapters {
		number := trimNumber(entry.Number)
		// The page lists a volume for every chapter, so the comic is split by the site's own
		// volumes instead of being cut into tens.
		volume := ""
		if entry.Volume > 0 {
			volume = trimNumber(entry.Volume)
		}
		label := entry.Title
		if volume != "" {
			label = "Том " + volume + " Глава " + number
			if entry.Title != "" && entry.Title != number {
				label += " " + entry.Title
			}
		}
		chapters = append(chapters, source.Chapter{
			Index:        entry.Number,
			Volume:       volume,
			Number:       number,
			Name:         entry.Title,
			DisplayTitle: label,
			Ref:          strconv.Itoa(entry.ID),
		})
	}
	return chapters, nil
}

// Pages implements source.MangaSource: the reader page carries the image list for its chapter.
func (s *Source) Pages(ctx context.Context, slug string, chapter source.Chapter) ([]source.Image, error) {
	data, err := s.dataFor(ctx, pagePath(slug))
	if err != nil {
		return nil, err
	}
	chapterID, err := strconv.Atoi(strings.TrimSpace(chapter.Ref))
	if err != nil || chapterID == 0 {
		return nil, fmt.Errorf("com-x: у главы %q нет идентификатора", chapter.DisplayTitle)
	}

	body, err := s.Client.get(ctx, fmt.Sprintf("%s/reader/%d/%d", s.Client.Base, data.NewsID, chapterID))
	if err != nil {
		return nil, err
	}
	reader, err := extractData(body)
	if err != nil {
		return nil, err
	}
	if len(reader.Images) == 0 {
		return nil, fmt.Errorf("com-x: глава %q без страниц", chapter.DisplayTitle)
	}

	host := reader.Host
	if host == "" {
		host = reader.HostRu
	}
	if strings.HasPrefix(host, "http") {
		// Defensive: the field is a bare host, but a full URL would still be usable.
	} else {
		host = "https://" + host
	}

	images := make([]source.Image, 0, len(reader.Images))
	for index, path := range reader.Images {
		url := host + "/comix/" + strings.TrimPrefix(path, "/")
		image, err := s.Client.FetchImage(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("com-x: страница %d: %w", index+1, err)
		}
		images = append(images, image)
	}
	return images, nil
}

func (s *Source) dataFor(ctx context.Context, path string) (pageData, error) {
	body, err := s.Client.get(ctx, s.Client.Base+"/"+path)
	if err != nil {
		return pageData{}, err
	}
	return extractData(body)
}

// FetchImage implements source.Source.
func (s *Source) FetchImage(ctx context.Context, rawURL string) (source.Image, error) {
	return s.Client.FetchImage(ctx, rawURL)
}

// normalizeSlug turns whatever came out of a link into a page path: the site keys titles by
// "<id>-<slug>.html".
func normalizeSlug(slug string) string {
	slug = strings.Trim(strings.TrimSpace(slug), "/")
	if index := strings.Index(slug, "://"); index >= 0 {
		rest := slug[index+3:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			slug = rest[slash+1:]
		}
	}
	// A reader link is /reader/<comic>/<chapter>; only the comic matters for the card.
	if strings.HasPrefix(slug, "reader/") {
		parts := strings.Split(slug, "/")
		if len(parts) >= 3 {
			return parts[1]
		}
	}
	if index := strings.IndexByte(slug, '#'); index >= 0 {
		slug = slug[:index]
	}
	if index := strings.IndexByte(slug, '?'); index >= 0 {
		slug = slug[:index]
	}
	return strings.TrimSuffix(slug, ".html")
}

// pagePath rebuilds the document name: the site serves titles at "<id>-<slug>.html" and answers a
// bare slug with its 404 page, which looks exactly like the JavaScript gate.
func pagePath(slug string) string {
	path := normalizeSlug(slug)
	if !strings.HasSuffix(path, ".html") {
		path += ".html"
	}
	return path
}

// extractData decodes window.__DATA__ out of a page.
func extractData(body []byte) (pageData, error) {
	const marker = "window.__DATA__ = "
	index := bytes.Index(body, []byte(marker))
	if index < 0 {
		return pageData{}, fmt.Errorf("com-x: в странице нет данных, вероятно нужны куки")
	}

	decoder := json.NewDecoder(bytes.NewReader(body[index+len(marker):]))
	var data pageData
	if err := decoder.Decode(&data); err != nil {
		return pageData{}, fmt.Errorf("com-x: не разобрал данные страницы: %w", err)
	}
	return data, nil
}

func trimNumber(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// imageSrc prefers the lazy attribute some themes use over the plain one.
func imageSrc(node *html.Node) string {
	for _, key := range []string{"data-src", "src"} {
		if value := attr(node, key); value != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// listValue reads a row of the "about" list, for example "Автор" -> name.
func listValue(document *html.Node, label string) string {
	list := findClass(document, "page__list", "")
	if list == nil {
		return ""
	}
	for item := list.FirstChild; item != nil; item = item.NextSibling {
		if item.Type != html.ElementNode || item.Data != "li" {
			continue
		}
		if !strings.Contains(textOf(item), label) {
			continue
		}
		if link := firstElement(item, "a"); link != nil {
			return textOf(link)
		}
		text := textOf(item)
		return strings.TrimSpace(strings.TrimPrefix(text, label))
	}
	return ""
}

func firstElement(root *html.Node, tag string) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found != nil {
			return
		}
		if node.Type == html.ElementNode && node.Data == tag {
			found = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return found
}

// findClass finds the first element with the given class, optionally limited to a descendant tag.
func findClass(root *html.Node, class, tag string) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found != nil {
			return
		}
		if node.Type == html.ElementNode && hasClass(node, class) {
			if tag == "" {
				found = node
				return
			}
			if child := firstElement(node, tag); child != nil {
				found = child
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return found
}

func hasClass(node *html.Node, class string) bool {
	for _, attribute := range node.Attr {
		if attribute.Key != "class" {
			continue
		}
		for _, part := range strings.Fields(attribute.Val) {
			if part == class {
				return true
			}
		}
	}
	return false
}

func attr(node *html.Node, key string) string {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, key) {
			return attribute.Val
		}
	}
	return ""
}

// textOf flattens an element into one line.
func textOf(node *html.Node) string {
	if node == nil {
		return ""
	}
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			out.WriteString(current.Data)
			return
		}
		if current.Type == html.ElementNode && (current.Data == "br" || current.Data == "p") {
			out.WriteString(" ")
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(out.String()), " ")
}
