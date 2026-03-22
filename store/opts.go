// Package store provides base types for the data access layer.
package store

// ListOpts is a standard pagination query option.
type ListOpts struct {
	Page    int
	Size    int
	OrderBy string // e.g. "id_desc", "created_at_asc"
}

// Offset returns the SQL OFFSET value.
func (o ListOpts) Offset() int {
	p := o.Page
	if p <= 0 {
		p = 1
	}
	return (p - 1) * o.PageSize()
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
