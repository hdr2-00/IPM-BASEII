package webapp

import (
	"embed"
	"io/fs"
)

// Pagination computes page bounds/labels for templates. Page numbers are
// 1-indexed to match what's shown in the UI.
type Pagination struct {
	Page       int
	PageSize   int
	Total      int
	TotalPages int
}

func newPagination(page, pageSize, total int) Pagination {
	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	return Pagination{Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages}
}

// StartIdx/EndIdx give the [start,end) slice bounds for the current page.
func (p Pagination) StartIdx() int {
	i := (p.Page - 1) * p.PageSize
	if i < 0 {
		return 0
	}
	if i > p.Total {
		return p.Total
	}
	return i
}

func (p Pagination) EndIdx() int {
	i := p.StartIdx() + p.PageSize
	if i > p.Total {
		return p.Total
	}
	return i
}

func (p Pagination) HasPrev() bool { return p.Page > 1 }
func (p Pagination) HasNext() bool { return p.Page < p.TotalPages }
func (p Pagination) PrevPage() int { return p.Page - 1 }
func (p Pagination) NextPage() int { return p.Page + 1 }

// RangeLabel renders "showing 1-25 of 137" style text.
func (p Pagination) RangeStart() int {
	if p.Total == 0 {
		return 0
	}
	return p.StartIdx() + 1
}
func (p Pagination) RangeEnd() int { return p.EndIdx() }

// fsSub returns the given subdirectory of an embed.FS as its own fs.FS, for
// serving static assets under /static/ without leaking the "static/" prefix
// into request paths.
func fsSub(f embed.FS, dir string) (fs.FS, error) {
	return fs.Sub(f, dir)
}

// FilterCounts is how many records fall into each of the records table's
// severity filters, so the filter chips can say how much they would show
// instead of making the operator click each one to find out.
type FilterCounts struct {
	Total    int
	Errors   int
	Warnings int
}

func (c *FilterCounts) add(row RecordRow) {
	c.Total++
	if row.Errors > 0 {
		c.Errors++
	}
	if row.Warnings > 0 {
		c.Warnings++
	}
}
