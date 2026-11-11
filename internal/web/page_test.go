package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

type pageRow struct{ n, total int64 }

// rows returns a load function over n rows that each carry the total, as
// the list queries' rows do.
func rows(n int) func(limit, offset int32) ([]pageRow, error) {
	return func(limit, offset int32) ([]pageRow, error) {
		var out []pageRow
		for i := int64(offset); i < int64(offset)+int64(limit) && i < int64(n); i++ {
			out = append(out, pageRow{n: i + 1, total: int64(n)})
		}
		return out, nil
	}
}

func TestLoadPage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		target    string
		matches   int
		number    int
		first     int64
		count     int
		wantRange string
	}{
		{"/admin/x", 120, 1, 1, 50, "1–50"},
		{"/admin/x?page=3", 120, 3, 101, 20, "101–120"},
		{"/admin/x?page=9", 120, 3, 101, 20, "101–120"}, // past the end
		{"/admin/x?page=2", 0, 1, 0, 0, "0–0"},
		{"/admin/x?page=-4", 10, 1, 1, 10, "1–10"},
		{"/admin/x?page=abc", 10, 1, 1, 10, "1–10"},
		{"/admin/x?page=99999999999", 60, 2, 51, 10, "51–60"},
	} {
		p := PageFrom(httptest.NewRequest(http.MethodGet, tc.target, nil), PageSize)
		got, err := LoadPage(&p, func(r pageRow) int64 { return r.total }, rows(tc.matches))
		if err != nil {
			t.Fatal(err)
		}
		if p.Number != tc.number || len(got) != tc.count || p.Total != int64(tc.matches) ||
			(tc.count > 0 && got[0].n != tc.first) {
			t.Errorf("%s over %d: page %d with %d rows, total %d; want page %d with %d rows from %d",
				tc.target, tc.matches, p.Number, len(got), p.Total, tc.number, tc.count, tc.first)
		}
		if r := fmt.Sprintf("%d–%d", p.First(), p.End()); r != tc.wantRange {
			t.Errorf("%s over %d: range %s, want %s", tc.target, tc.matches, r, tc.wantRange)
		}
	}
}

func TestPageLinks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		number, last int
		want         []int
	}{
		{1, 1, []int{1}},
		{1, 3, []int{1, 2, 3}},
		{1, 10, []int{1, 2, 0, 10}},
		{5, 10, []int{1, 0, 4, 5, 6, 0, 10}},
		{3, 10, []int{1, 2, 3, 4, 0, 10}},
		{10, 10, []int{1, 0, 9, 10}},
	} {
		p := Page{Number: tc.number, Size: 10, Total: int64(tc.last * 10)}
		if got := p.Links(); !slices.Equal(got, tc.want) {
			t.Errorf("page %d of %d: links %v, want %v", tc.number, tc.last, got, tc.want)
		}
	}
}

func TestPageURL(t *testing.T) {
	t.Parallel()
	p := PageFrom(httptest.NewRequest(http.MethodGet, "/admin/x?q=red+tee&show=out&page=4", nil), PageSize)
	if got := p.URL(2); got != "/admin/x?page=2&q=red+tee&show=out" {
		t.Errorf("URL(2) = %s", got)
	}
	if got := p.URL(1); got != "/admin/x?q=red+tee&show=out" {
		t.Errorf("URL(1) = %s", got)
	}
}

func TestCount(t *testing.T) {
	t.Parallel()
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 3989: "3,989", 1234567: "1,234,567", -1000: "-1,000"} {
		if got := Count(n); got != want {
			t.Errorf("Count(%d) = %s, want %s", n, got, want)
		}
	}
}
