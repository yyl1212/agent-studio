package database

import (
	"context"
	"strings"
	"testing"
)

func TestOpenPoolRedactsDatabaseURLOnFailure(t *testing.T) {
	const secret = "sentinel-password"
	_, err := OpenPool(context.Background(), "postgres://agent:"+secret+"@127.0.0.1:1/db?sslmode=disable")
	if err == nil {
		t.Fatal("OpenPool() error = nil")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("OpenPool() exposed database URL: %v", err)
	}
}

func TestOpenPoolRedactsInvalidConfiguration(t *testing.T) {
	const secret = "sentinel-password"
	_, err := OpenPool(context.Background(), "postgres://agent:"+secret+"@[")
	if err == nil {
		t.Fatal("OpenPool() error = nil")
	}
	if got := err.Error(); got != "parse database configuration" {
		t.Fatalf("OpenPool() error = %q", got)
	}
}
