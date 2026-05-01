package sqlite

import "time"

// unixOrNil returns t.Unix() if t is non-zero, otherwise nil for SQL NULL.
func unixOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

// unixToTime converts a stored unix timestamp into a UTC time.Time.
func unixToTime(sec int64) time.Time {
	return time.Unix(sec, 0).UTC()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// scanner matches both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// nullableString returns nil for an empty string (stored as SQL NULL).
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ptrUnix returns nil if t is nil, otherwise the Unix timestamp.
func ptrUnix(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}
