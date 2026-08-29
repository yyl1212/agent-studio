package backup

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestV1Alpha1TableOrderAndLimitsAreStable(t *testing.T) {
	want := []TableName{
		TableWorkflows,
		TableWorkflowVersions,
		TableRuns,
		TableNodeRuns,
		TableRunEvents,
		TableWorkflowDraftCheckpoints,
	}
	if !slices.Equal(TableOrder, want) {
		t.Fatalf("TableOrder = %v; want %v", TableOrder, want)
	}
	if APIVersion != "agent-studio.dev/backup/v1alpha1" || MaxManifestBytes != 1<<20 ||
		MaxChecksumsBytes != 1<<20 || MaxCentralDirectoryBytes != 1<<20 ||
		MaxRecordBytes != 16<<20 || MaxArchiveBytes != 64<<30 {
		t.Fatalf("version=%q manifest=%d checksums=%d central=%d record=%d archive=%d",
			APIVersion, MaxManifestBytes, MaxChecksumsBytes, MaxCentralDirectoryBytes, MaxRecordBytes, MaxArchiveBytes)
	}
}

func TestTablePathMapsOnlyPublishedTableNames(t *testing.T) {
	want := map[TableName]string{
		TableWorkflows:                "data/workflows.jsonl",
		TableWorkflowVersions:         "data/workflow_versions.jsonl",
		TableRuns:                     "data/runs.jsonl",
		TableNodeRuns:                 "data/node_runs.jsonl",
		TableRunEvents:                "data/run_events.jsonl",
		TableWorkflowDraftCheckpoints: "data/workflow_draft_checkpoints.jsonl",
	}
	for name, expected := range want {
		got, err := tablePath(name)
		if err != nil || got != expected {
			t.Fatalf("tablePath(%q) = %q, %v; want %q, nil", name, got, err, expected)
		}
	}
	for _, name := range []TableName{"", "../runs", "unknown"} {
		if _, err := tablePath(name); err == nil {
			t.Fatalf("tablePath(%q) error = nil", name)
		}
	}
}

func TestManifestUsesPublishedJSONFieldNames(t *testing.T) {
	manifest := Manifest{
		APIVersion:               APIVersion,
		CreatedAt:                time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC),
		RuntimeVersion:           "0.5.0-test",
		DatabaseMigrationVersion: 6,
		IncludesRuns:             true,
		DatasetDigest:            "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Tables: []TableManifest{{
			Name: TableWorkflows, Path: "data/workflows.jsonl", Records: 2,
			UncompressedBytes: 128, Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		}},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"apiVersion":"agent-studio.dev/backup/v1alpha1","createdAt":"2026-08-29T00:00:00Z","runtimeVersion":"0.5.0-test","databaseMigrationVersion":6,"includesRuns":true,"datasetDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","tables":[{"name":"workflows","path":"data/workflows.jsonl","records":2,"uncompressedBytes":128,"digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}`
	if string(encoded) != want {
		t.Fatalf("json.Marshal(Manifest) = %s; want %s", encoded, want)
	}
}
