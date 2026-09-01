// Package store provides base types for the data access layer.
package store

import (
	"math"
	"strings"
)

// ListOpts is a standard pagination query option.
type ListOpts struct {
	Page    int
	Size    int
	OrderBy string // e.g. "id_desc", "created_at_asc"
}

// SafeOrderBy resolves a user-facing sort key through an explicit allowlist.
// It returns an empty clause for unknown keys; callers should then use a
// deterministic default. The raw OrderBy field must never be concatenated into
// SQL directly.
func (o ListOpts) SafeOrderBy(allowed map[string]string, fallback string) string {
	key := strings.ToLower(strings.TrimSpace(o.OrderBy))
	if clause, ok := allowed[key]; ok {
		return clause
	}
	return fallback
}

// Offset returns the SQL OFFSET value.
func (o ListOpts) Offset() int {
	p := o.Page
	if p <= 0 {
		p = 1
	}
	size := o.PageSize()
	if p-1 > math.MaxInt/size {
		return math.MaxInt
	}
	return (p - 1) * size
}

// PageSize returns the SQL LIMIT value (default 20, max 500).
func (o ListOpts) PageSize() int {
	if o.Size <= 0 {
		return 20
	}
	if o.Size > 500 {
		return 500
	}
	return o.Size
}
