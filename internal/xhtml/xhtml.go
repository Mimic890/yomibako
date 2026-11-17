// Package xhtml converts the HTML that sites hand out in chapter bodies into clean XHTML that an
// EPUB may contain: known tags only, attributes trimmed, images pointed at local files.
package xhtml

import (
	"html"
	"strings"

	xhtml "golang.org/x/net/html"
)

// Options tells the converter what to do with images.
type Options struct {
	// ResolveImage maps the original src to the href written into the XHTML. An empty result
	// drops the image.
	ResolveImage func(src string) string
	// OnImage is called for every image src that survived, so the caller can download it.
	OnImage func(src string)
}

// allowed lists the tags kept in the output. Anything else is unwrapped: its children stay, the
// element itself disappears, so no content is lost.
var allowed = map[string]bool{
	"p": true, "br": true, "hr": true,
	"em": true, "i": true, "strong": true, "b": true, "u": true, "s": true, "sub": true, "sup": true,
	"blockquote": true, "cite": true, "code": true, "pre": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "li": true,
	"table": true, "thead": true, "tbody": true, "tr": true, "td": true, "th": true,
	"img": true, "a": true,
}

// selfClosing are written as <tag/>.
var selfClosing = map[string]bool{"br": true, "hr": true, "img": true}

// dropped are removed with their whole subtree.
var dropped = map[string]bool{
	"script": true, "style": true, "iframe": true, "noscript": true, "form": true,
	"ins": true, "input": true, "button": true, "svg": true, "video": true, "audio": true,
}

// FromHTML converts an HTML fragment. It never fails: a fragment that does not parse comes back
// as escaped text.
func FromHTML(fragment string, options Options) string {
	nodes, err := xhtml.ParseFragment(strings.NewReader(fragment), &xhtml.Node{
		Type:     xhtml.ElementNode,
		Data:     "div",
		DataAtom: 0,
	})
	if err != nil {
		return "<p>" + html.EscapeString(stripTags(fragment)) + "</p>"
	}

	var out strings.Builder
	for _, node := range nodes {
		writeNode(&out, node, options)
	}

	body := strings.TrimSpace(out.String())
	if body == "" {
		text := strings.TrimSpace(stripTags(fragment))
		if text == "" {
			return ""
		}
		return "<p>" + html.EscapeString(text) + "</p>"
	}
	return body
}

func writeNode(out *strings.Builder, node *xhtml.Node, options Options) {
	switch node.Type {
	case xhtml.TextNode:
		out.WriteString(html.EscapeString(collapse(node.Data)))
		return
	case xhtml.ElementNode:
	default:
		return
	}

	tag := strings.ToLower(node.Data)
	if dropped[tag] {
		return
	}

	// A div is only a layout box; inside a novel body it means a paragraph.
	if tag == "div" || tag == "section" || tag == "article" {
		out.WriteString("<p>")
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			writeNode(out, child, options)
		}
		out.WriteString("</p>")
		return
	}

	if !allowed[tag] {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			writeNode(out, child, options)
		}
		return
	}

	out.WriteString("<" + tag)
	writeAttributes(out, tag, node, options)
	if selfClosing[tag] {
		out.WriteString("/>")
		return
	}
	out.WriteString(">")
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		writeNode(out, child, options)
	}
	out.WriteString("</" + tag + ">")
}

func writeAttributes(out *strings.Builder, tag string, node *xhtml.Node, options Options) {
	switch tag {
	case "img":
		raw := attribute(node, "data-src")
		if raw == "" {
			raw = attribute(node, "src")
		}
		if raw == "" || strings.HasPrefix(raw, "data:") {
			return
		}
		href := raw
		if options.ResolveImage != nil {
			href = options.ResolveImage(raw)
		}
		if href == "" {
			return
		}
		if options.OnImage != nil {
			options.OnImage(raw)
		}
		alt := attribute(node, "alt")
		if alt == "" {
			alt = "иллюстрация"
		}
		out.WriteString(` src="` + html.EscapeString(href) + `" alt="` + html.EscapeString(alt) + `"`)
	case "a":
		href := attribute(node, "href")
		if href == "" || strings.HasPrefix(href, "#") {
			return
		}
		if !strings.HasPrefix(href, "http://") && !strings.HasPrefix(href, "https://") {
			return
		}
		out.WriteString(` href="` + html.EscapeString(href) + `"`)
	}
}

func attribute(node *xhtml.Node, name string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

// collapse turns runs of whitespace that HTML ignores into a single space, keeping line breaks out
// of the markup: the layout belongs to the stylesheet.
func collapse(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// stripTags is the fallback and also used for summaries.
func stripTags(value string) string {
	var out strings.Builder
	depth := 0
	for _, r := range value {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				out.WriteRune(r)
			}
		}
	}
	return html.UnescapeString(out.String())
}

// Text turns an HTML fragment into plain text, used for summaries and descriptions.
func Text(fragment string) string {
	nodes, err := xhtml.ParseFragment(strings.NewReader(fragment), &xhtml.Node{Type: xhtml.ElementNode, Data: "div"})
	if err != nil {
		return strings.TrimSpace(stripTags(fragment))
	}

	var out []string
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.TextNode {
			if text := strings.TrimSpace(node.Data); text != "" {
				out = append(out, collapse(text))
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if node.Type == xhtml.ElementNode && (node.Data == "p" || node.Data == "div" || node.Data == "br") {
			out = append(out, "\n")
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	return strings.TrimSpace(strings.Join(out, " "))
}

// InnerHTML returns the raw inner markup of the first element whose class contains want. It is
// how a chapter body is cut out of a full page before FromHTML sanitises it.
func InnerHTML(document, want string) string {
	root, err := xhtml.Parse(strings.NewReader(document))
	if err != nil {
		return ""
	}

	var found *xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if found != nil {
			return
		}
		if node.Type == xhtml.ElementNode && hasClass(node, want) {
			found = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if found == nil {
		return ""
	}

	var out strings.Builder
	for child := found.FirstChild; child != nil; child = child.NextSibling {
		_ = xhtml.Render(&out, child)
	}
	return out.String()
}

func hasClass(node *xhtml.Node, want string) bool {
	for _, attr := range node.Attr {
		if attr.Key != "class" {
			continue
		}
		for _, part := range strings.Fields(attr.Val) {
			if part == want {
				return true
			}
		}
	}
	return false
}

// Title returns the text of the first <title> element, or the og:title meta value.
func Title(document string) string {
	root, err := xhtml.Parse(strings.NewReader(document))
	if err != nil {
		return ""
	}

	var title, og string
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode {
			switch node.Data {
			case "title":
				if title == "" && node.FirstChild != nil {
					title = strings.TrimSpace(node.FirstChild.Data)
				}
			case "meta":
				if og == "" && attribute(node, "property") == "og:title" {
					og = attribute(node, "content")
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if title != "" {
		return title
	}
	return og
}
