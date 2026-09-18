package scraper

import (
	"strings"

	"golang.org/x/net/html"
)

// attr returns the value of the named attribute, or "".
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// hasClass reports whether the node's class attribute contains the given token.
func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

// matches reports whether n is an element with the given tag and (optionally) class.
func matches(n *html.Node, tag, class string) bool {
	if n.Type != html.ElementNode || n.Data != tag {
		return false
	}
	return class == "" || hasClass(n, class)
}

// findAll returns every descendant element matching tag (+optional class).
func findAll(root *html.Node, tag, class string) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if matches(n, tag, class) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// findFirst returns the first descendant element matching tag (+optional class), or nil.
func findFirst(root *html.Node, tag, class string) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if matches(n, tag, class) {
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

// text returns the concatenated, trimmed text content of a node subtree.
func text(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}
