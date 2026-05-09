package postgres

import "time"

// timeOrNil returns t if t is non-zero, otherwise nil for SQL NULL. Postgres
// TIMESTAMPTZ binds time.Time directly; nullable columns need an interface{}
// nil to drive a NULL.
func timeOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// ptrTimeOrNil returns nil if t is nil or zero, otherwise the underlying time.
func ptrTimeOrNil(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return *t
}

// nullableString returns nil for an empty string (stored as SQL NULL).
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// scanner matches both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}
