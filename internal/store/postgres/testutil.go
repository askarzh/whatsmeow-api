package postgres

import (
	"context"
	"os/exec"
	"sync"
	"testing"
	"time"

	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	testImage    = "postgres:16-alpine"
	testDatabase = "whatsmeow_api_test"
	testUser     = "test"
	testPassword = "test"
)

var (
	dockerCheckOnce sync.Once
	dockerOK        bool
)

func dockerAvailable() bool {
	dockerCheckOnce.Do(func() {
		err := exec.Command("docker", "info").Run()
		dockerOK = err == nil
	})
	return dockerOK
}

// NewTestStore boots a postgres:16-alpine container, runs migrations, and
// returns a connected *Store. Skips the calling test if Docker is unavailable.
// The container and *Store are torn down via t.Cleanup.
func NewTestStore(t *testing.T) *Store {
	t.Helper()
	if !dockerAvailable() {
		t.Skip("postgres tests require docker")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	container, err := pgcontainer.Run(ctx, testImage,
		pgcontainer.WithDatabase(testDatabase),
		pgcontainer.WithUsername(testUser),
		pgcontainer.WithPassword(testPassword),
		pgcontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get container DSN: %v", err)
	}

	s, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
	})

	return s
}

// resetTables truncates every table in the public schema. Tests use this to
// isolate from each other without paying for a new container per test.
func resetTables(t *testing.T, s *Store) {
	t.Helper()
	const stmt = `
		TRUNCATE TABLE
			receipts, reactions, kv, events_log,
			media, messages, contacts, chats
		RESTART IDENTITY CASCADE
	`
	if _, err := s.db.ExecContext(context.Background(), stmt); err != nil {
		t.Fatalf("reset tables: %v", err)
	}
}
