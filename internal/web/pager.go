package web

import (
	"net/http"
	"net/url"
	"strconv"
)

// pager supports "previous/next" links that keep the current filters.
type pager struct {
	Page, PerPage, Total int
	query                url.Values
	path                 string
}

func newPager(r *http.Request, perPage, total int) pager {
	return pager{Page: pageFrom(r), PerPage: perPage, Total: total, query: r.URL.Query(), path: r.URL.Path}
}

// maxPage keeps page × per page far from overflowing an offset.
const maxPage = 1 << 20

func pageFrom(r *http.Request) int {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	return min(max(1, page), maxPage)
}

func (p pager) Offset() int { return (p.Page - 1) * p.PerPage }

func (p pager) Pages() int { return max(1, (p.Total+p.PerPage-1)/p.PerPage) }

func (p pager) First() int { return min(p.Total, p.Offset()+1) }

func (p pager) Last() int { return min(p.Total, p.Offset()+p.PerPage) }

func (p pager) PrevURL() string {
	if p.Page <= 1 {
		return ""
	}
	return p.url(p.Page - 1)
}

func (p pager) NextURL() string {
	if p.Page >= p.Pages() {
		return ""
	}
	return p.url(p.Page + 1)
}

// Beyond is a page past the end of a list that has something in it, as
// an old link to a list since shortened is.
func (p pager) Beyond() bool { return p.Total > 0 && p.Page > p.Pages() }

// LastURL is the list's last page.
func (p pager) LastURL() string { return p.url(p.Pages()) }

func (p pager) url(page int) string {
	q := url.Values{}
	for k, v := range p.query {
		q[k] = v
	}
	q.Set("page", strconv.Itoa(page))
	// Back to the list itself: the anchor puts the scroll and the keyboard focus
	// at its top, not at the top of the page above the filters.
	return p.path + "?" + q.Encode() + "#list"
}

// facetKey is a list's query for memoising its facet counts (store.Memo):
// only the filters (names) its counts depend on, each as the list reads it,
// so the page, order or any made-up parameter can't make a new entry and
// push the real ones out. The rest the counts depend on (ignored programs,
// tags) is in the database, whose every change starts the memo again.
func facetKey(q url.Values, names ...string) string {
	k := url.Values{}
	for _, name := range names {
		if v := q.Get(name); v != "" {
			k.Set(name, v)
		}
	}
	return k.Encode()
}

// programFacetParams and disclosureFacetParams are what programFilterFrom
// and disclosureFilterFrom read, less the order.
var (
	programFacetParams = []string{"platform", "visibility", "invite", "state", "bounty", "creds", "q", "resumed", "ignored", "tag", "top",
		"new", "launched", "wild", "apps", "api", "noreq", "watch", "noted", "not"}
	disclosureFacetParams = []string{"platform", "q", "severity", "weakness", "paid", "since"}
)
