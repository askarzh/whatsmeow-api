package postgres_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/store/postgres"
	"github.com/stretchr/testify/require"
)

func TestNewRunsMigrationsAndCloses(t *testing.T) {
	s := postgres.NewTestStore(t)

	// If we got this far, the container booted, migrations ran without error,
	// and the *Store is non-nil. Sub-store fields are nil until later tasks;
	// that's expected. Bundle() should still return a zero-valued struct
	// without panicking.
	bundle := s.Bundle()
	_ = bundle

	// Closing should be clean.
	require.NoError(t, s.Close())
}
