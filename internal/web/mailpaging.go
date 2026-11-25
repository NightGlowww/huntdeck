package web

import (
	"cmp"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// The Email page's links: its pages (HuntMail pages with a cursor that
// only goes older, so a page's address carries the ones before it), sizes
// and filters.

// pager sets the paging links.
func (v *mailView) pager() {
	if v.next != "" {
		back := append(slices.Clone(v.back), cmp.Or(v.cursor, "0"))
		if len(back) > mailBackMax {
			back = back[len(back)-mailBackMax:]
		}
		v.NextURL = v.link(v.next, back, v.Size)
	}
	if v.Page > 1 {
		v.FirstURL = v.link("", nil, v.Size)
		prev := ""
		if n := len(v.back); n > 0 {
			prev = v.back[n-1]
		}
		if prev == "0" || prev == "" {
			v.PrevURL = v.FirstURL
		} else {
			v.PrevURL = v.link(prev, v.back[:len(v.back)-1], v.Size)
		}
	}
}

// link is the list's address with its filters, at a page and size.
func (v *mailView) link(cursor string, back []string, size int) string {
	q := v.filters()
	if size != mailPageSize {
		q.Set("n", strconv.Itoa(size))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
		if len(back) > 0 {
			q.Set("back", strings.Join(back, ","))
		}
	}
	if len(q) == 0 {
		return "/email"
	}
	return "/email?" + q.Encode()
}

func (v *mailView) filters() url.Values {
	q := url.Values{}
	if v.Q != "" {
		q.Set("q", v.Q)
	}
	if v.To != "" {
		q.Set("to", v.To)
	}
	if v.Domain != "" {
		q.Set("domain", v.Domain)
	}
	if v.Program != nil {
		q.Set("program", strconv.FormatInt(v.Program.ProgramID, 10))
	}
	if v.NL != "" {
		q.Set("nl", v.NL)
	}
	return q
}

// SizeURL is the list's first page at another page size.
func (v *mailView) SizeURL(n int) string { return v.link("", nil, n) }

// FilterOn is the list with key ("q", "to", "domain", "program", "nl") set to
// value, or dropped when value is "", its other filters and page size kept,
// its paging dropped. With key "", it's the list's first page.
func (v *mailView) FilterOn(key, value string) string {
	q := v.filters()
	if key != "" {
		q.Del(key)
		if value != "" {
			q.Set(key, value)
		}
	}
	if v.Size != mailPageSize {
		q.Set("n", strconv.Itoa(v.Size))
	}
	if len(q) == 0 {
		return "/email"
	}
	return "/email?" + q.Encode()
}

// ProgramID is the id the list keeps to, as text, "" for none.
func (v *mailView) ProgramID() string {
	if v.Program == nil {
		return ""
	}
	return strconv.FormatInt(v.Program.ProgramID, 10)
}
