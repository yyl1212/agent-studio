package main

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestWorkerOwnerIDIsUniqueAndContainsNoConfiguration(t *testing.T) {
	first, second := workerOwnerID(), workerOwnerID()
	if first == second || strings.Contains(first, "postgres://") || strings.Contains(first, "RUN_PAYLOAD_ENCRYPTION_KEY") {
		t.Fatalf("unsafe worker IDs: %q %q", first, second)
	}
	parts := strings.Split(first, ":")
	if len(parts) != 3 {
		t.Fatalf("worker ID=%q", first)
	}
	if _, err := uuid.Parse(parts[2]); err != nil {
		t.Fatalf("worker ID UUID=%q error=%v", parts[2], err)
	}
}
