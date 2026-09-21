package webtoons

import (
	"strings"

	"golang.org/x/net/html"
)

// This file holds the small DOM-query helpers the WEBTOON scraper needs.
// They deliberately stay minimal — enough to walk a parsed page and pull out
// nodes by class, tag or attribute — so the client keeps its only HTML
// dependency on golang.org/x/net/html.

// matcher reports whether a node is of interest.
type matcher func(*html.Node) bool

// attr returns the value of the named attribute, or "" when absent.
func attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// hasClass builds a matcher for elements carrying the given CSS class.
func hasClass(class string) matcher {
	return func(n *html.Node) bool {
		if n == nil || n.Type != html.ElementNode {
			return false
		}
		return strings.Contains(" "+attr(n, "class")+" ", " "+class+" ")
	}
}

// hasTag builds a matcher for elements with the given tag name.
func hasTag(tag string) matcher {
	return func(n *html.Node) bool {
		return n != nil && n.Type == html.ElementNode && n.Data == tag
	}
}

// all combines matchers with a logical AND.
func all(ms ...matcher) matcher {
	return func(n *html.Node) bool {
		for _, m := range ms {
			if !m(n) {
				return false
			}
		}
		return true
	}
}

// findFirst returns the first node in document order matching m, or nil.
func findFirst(root *html.Node, m matcher) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil || n == nil {
			return
		}
		if m(n) {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}

// findAll returns every node matching m, in document order.
// Matching nodes are not descended into, so nested matches are not returned
// twice — which is what the list-item and card queries want.
func findAll(root *html.Node, m matcher) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if m(n) {
			out = append(out, n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// text returns the whitespace-collapsed concatenation of every text node under n.
func text(n *html.Node) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(sb.String()), " ")
}

// textOfClass returns the text of the first descendant carrying the given class.
func textOfClass(root *html.Node, class string) string {
	return text(findFirst(root, hasClass(class)))
}

// metaContent returns the content of the first <meta> whose property or name
// attribute equals key (e.g. "og:title").
func metaContent(root *html.Node, key string) string {
	n := findFirst(root, func(n *html.Node) bool {
		if n == nil || n.Type != html.ElementNode || n.Data != "meta" {
			return false
		}
		return attr(n, "property") == key || attr(n, "name") == key
	})
	return strings.TrimSpace(attr(n, "content"))
}

// imgSrc returns the src of the first <img> under root.
func imgSrc(root *html.Node) string {
	return attr(findFirst(root, hasTag("img")), "src")
}
