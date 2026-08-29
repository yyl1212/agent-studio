package postgres

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestOpenRedactsInvalidDatabaseURL(t *testing.T) {
	const secret = "sentinel-password"
	_, err := Open(context.Background(), "postgres://agent:"+secret+"@[")
	if err == nil {
		t.Fatal("Open() error = nil")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("Open() exposed database URL: %v", err)
	}
}

func TestReadyReportsVersionForEmptySchema(t *testing.T) {
	store := openUnmigratedTestStore(t)
	err := store.Ready(context.Background())
	if err == nil || err.Error() != "database migration version 0, want 6" {
		t.Fatalf("Ready() error = %v", err)
	}
}

func openUnmigratedTestStore(t *testing.T) *Store {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	databaseTestMutex.Lock()
	admin, err := Open(context.Background(), databaseURL)
	if err != nil {
		databaseTestMutex.Unlock()
		t.Fatal(err)
	}
	schema := fmt.Sprintf("store_ready_%d", fixtureSequence.Add(1))
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.pool.Exec(context.Background(), "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		databaseTestMutex.Unlock()
		t.Fatal(err)
	}
	var store *Store
	t.Cleanup(func() {
		if store != nil {
			store.Close()
		}
		_, _ = admin.pool.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		databaseTestMutex.Unlock()
	})
	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsedURL.Query()
	query.Set("options", "-csearch_path="+schema)
	parsedURL.RawQuery = query.Encode()
	store, err = Open(context.Background(), parsedURL.String())
	if err != nil {
		t.Fatal(err)
	}
	return store
}
