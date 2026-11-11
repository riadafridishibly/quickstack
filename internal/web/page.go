package web

import (
	"math"
	"net/http"
	"net/url"
	"strconv"
)

// PageSize is the number of rows on one page of an admin list.
const PageSize = 50

// Page is one page of a list, read from ?page=. Total is the number of
// matching rows, set by LoadPage.
type Page struct {
	Number int
	Size   int
	Total  int64
	url    url.URL
}

// PageFrom reads ?page= from r. A missing or invalid number is page 1.
func PageFrom(r *http.Request, size int) Page { return PageAt(*r.URL, size) }

// PageAt is PageFrom for the request URL u.
func PageAt(u url.URL, size int) Page {
	n, err := strconv.Atoi(u.Query().Get("page"))
	if err != nil || n < 1 {
		n = 1
	}
	return Page{Number: min(n, math.MaxInt32/size), Size: size, url: u}
}

func (p Page) Limit() int32  { return int32(p.Size) }
func (p Page) Offset() int32 { return int32((p.Number - 1) * p.Size) }

// Last is the number of the last page, at least 1.
func (p Page) Last() int { return max(1, int((p.Total+int64(p.Size)-1)/int64(p.Size))) }

// First and End are the 1-based positions of the page's first and last
// rows.
func (p Page) First() int64 { return min(int64(p.Offset())+1, p.Total) }
func (p Page) End() int64   { return min(int64(p.Offset())+int64(p.Size), p.Total) }

// URL is the request's URL with page n, keeping its other parameters.
func (p Page) URL(n int) string {
	u := p.url
	q := u.Query()
	if n > 1 {
		q.Set("page", strconv.Itoa(n))
	} else {
		q.Del("page")
	}
	u.RawQuery = q.Encode()
	return u.RequestURI()
}

// Links lists the page numbers the pager shows: the first, the last, and
// the current page with its neighbours. 0 marks a gap.
func (p Page) Links() []int {
	last := p.Last()
	var links []int
	for n := 1; n <= last; n++ {
		if n == 1 || n == last || (n >= p.Number-1 && n <= p.Number+1) {
			links = append(links, n)
		} else if links[len(links)-1] != 0 {
			links = append(links, 0)
		}
	}
	return links
}

// LoadPage loads p's rows with load, and sets p.Total from the count that
// each row carries. A page past the end, such as one emptied by a change,
// loads the last page instead.
func LoadPage[T any](p *Page, total func(T) int64, load func(limit, offset int32) ([]T, error)) ([]T, error) {
	rows, err := load(p.Limit(), p.Offset())
	if err == nil && len(rows) == 0 && p.Number > 1 {
		var first []T
		if first, err = load(1, 0); err == nil && len(first) > 0 {
			p.Total = total(first[0])
			p.Number = p.Last()
			rows, err = load(p.Limit(), p.Offset())
		} else {
			p.Number = 1
		}
	}
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		p.Total = total(rows[0])
	}
	return rows, nil
}

// Count formats n with thousands separators, such as 3,989.
func Count(n int64) string {
	s := strconv.FormatInt(n, 10)
	start := 0
	if n < 0 {
		start = 1
	}
	for i := len(s) - 3; i > start; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
