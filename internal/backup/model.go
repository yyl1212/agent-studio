package backup

import (
	"errors"
	"time"
)

const (
	APIVersion                      = "agent-studio.dev/backup/v1alpha1"
	MaxManifestBytes                = 1 << 20
	MaxChecksumsBytes               = 1 << 20
	MaxCentralDirectoryBytes        = 1 << 20
	MaxRecordBytes                  = 16 << 20
	MaxArchiveBytes          uint64 = 64 << 30
)

type TableName string

const (
	TableWorkflows                TableName = "workflows"
	TableWorkflowVersions         TableName = "workflow_versions"
	TableRuns                     TableName = "runs"
	TableNodeRuns                 TableName = "node_runs"
	TableRunEvents                TableName = "run_events"
	TableWorkflowDraftCheckpoints TableName = "workflow_draft_checkpoints"
)

var TableOrder = []TableName{
	TableWorkflows,
	TableWorkflowVersions,
	TableRuns,
	TableNodeRuns,
	TableRunEvents,
	TableWorkflowDraftCheckpoints,
}

type TableManifest struct {
	Name              TableName `json:"name"`
	Path              string    `json:"path"`
	Records           uint64    `json:"records"`
	UncompressedBytes uint64    `json:"uncompressedBytes"`
	Digest            string    `json:"digest"`
}

type Manifest struct {
	APIVersion               string          `json:"apiVersion"`
	CreatedAt                time.Time       `json:"createdAt"`
	RuntimeVersion           string          `json:"runtimeVersion"`
	DatabaseMigrationVersion int64           `json:"databaseMigrationVersion"`
	IncludesRuns             bool            `json:"includesRuns"`
	DatasetDigest            string          `json:"datasetDigest"`
	Tables                   []TableManifest `json:"tables"`
}

type Summary struct {
	Path              string          `json:"path"`
	APIVersion        string          `json:"apiVersion"`
	CreatedAt         time.Time       `json:"createdAt"`
	RuntimeVersion    string          `json:"runtimeVersion"`
	MigrationVersion  int64           `json:"databaseMigrationVersion"`
	DatasetDigest     string          `json:"datasetDigest"`
	CompressedBytes   int64           `json:"compressedBytes"`
	UncompressedBytes uint64          `json:"uncompressedBytes"`
	Tables            []TableManifest `json:"tables"`
}

func tablePath(name TableName) (string, error) {
	switch name {
	case TableWorkflows:
		return "data/workflows.jsonl", nil
	case TableWorkflowVersions:
		return "data/workflow_versions.jsonl", nil
	case TableRuns:
		return "data/runs.jsonl", nil
	case TableNodeRuns:
		return "data/node_runs.jsonl", nil
	case TableRunEvents:
		return "data/run_events.jsonl", nil
	case TableWorkflowDraftCheckpoints:
		return "data/workflow_draft_checkpoints.jsonl", nil
	default:
		return "", errors.New("unknown backup table")
	}
}
