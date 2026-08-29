package backup

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
)

type WorkflowRecord struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Slug               string          `json:"slug"`
	Description        string          `json:"description"`
	DraftGraph         json.RawMessage `json:"draftGraph"`
	DraftRevision      int64           `json:"draftRevision"`
	PublishedVersionID *string         `json:"publishedVersionId"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`
	ArchivedAt         *time.Time      `json:"archivedAt"`
	AgentPresentation  json.RawMessage `json:"agentPresentation"`
}

type WorkflowVersionRecord struct {
	ID                string          `json:"id"`
	WorkflowID        string          `json:"workflowId"`
	Version           int             `json:"version"`
	Graph             json.RawMessage `json:"graph"`
	InputSchema       json.RawMessage `json:"inputSchema"`
	CreatedAt         time.Time       `json:"createdAt"`
	AgentPresentation json.RawMessage `json:"agentPresentation"`
}

type RunRecord struct {
	ID                 string           `json:"id"`
	WorkflowID         string           `json:"workflowId"`
	WorkflowVersionID  *string          `json:"workflowVersionId"`
	DraftRevision      *int64           `json:"draftRevision"`
	GraphSnapshot      *json.RawMessage `json:"graphSnapshot"`
	Mode               string           `json:"mode"`
	Status             string           `json:"status"`
	Input              json.RawMessage  `json:"input"`
	Output             *json.RawMessage `json:"output"`
	Error              *json.RawMessage `json:"error"`
	StartedAt          time.Time        `json:"startedAt"`
	EndedAt            *time.Time       `json:"endedAt"`
	SourceRunID        *string          `json:"sourceRunId"`
	SourceNodeID       *string          `json:"sourceNodeId"`
	RetryOfRunID       *string          `json:"retryOfRunId"`
	RetryKey           *string          `json:"retryKey"`
	InputRedactedPaths []string         `json:"inputRedactedPaths"`
	CancelRequestedAt  *time.Time       `json:"cancelRequestedAt"`
	HeartbeatAt        *time.Time       `json:"heartbeatAt"`
	AgentRequestKey    *string          `json:"agentRequestKey"`
}

type NodeRunRecord struct {
	ID        string           `json:"id"`
	RunID     string           `json:"runId"`
	NodeID    string           `json:"nodeId"`
	NodeType  string           `json:"nodeType"`
	Status    string           `json:"status"`
	Input     *json.RawMessage `json:"input"`
	Output    *json.RawMessage `json:"output"`
	Error     *json.RawMessage `json:"error"`
	StartedAt *time.Time       `json:"startedAt"`
	EndedAt   *time.Time       `json:"endedAt"`
}

type RunEventRecord struct {
	RunID               string           `json:"runId"`
	Sequence            int64            `json:"sequence"`
	Type                string           `json:"type"`
	NodeID              *string          `json:"nodeId"`
	Status              *string          `json:"status"`
	Input               *json.RawMessage `json:"input"`
	Output              *json.RawMessage `json:"output"`
	ActivePorts         []string         `json:"activePorts"`
	Error               *json.RawMessage `json:"error"`
	InputRedactedPaths  []string         `json:"inputRedactedPaths"`
	OutputRedactedPaths []string         `json:"outputRedactedPaths"`
	DataBytes           int64            `json:"dataBytes"`
	Timestamp           time.Time        `json:"timestamp"`
}

type WorkflowDraftCheckpointRecord struct {
	WorkflowID            string          `json:"workflowId"`
	SourceRevision        int64           `json:"sourceRevision"`
	RestoredRevision      int64           `json:"restoredRevision"`
	Graph                 json.RawMessage `json:"graph"`
	AgentPresentation     json.RawMessage `json:"agentPresentation"`
	RestoredFromVersionID string          `json:"restoredFromVersionId"`
	CreatedAt             time.Time       `json:"createdAt"`
}

var recordFields = map[TableName][]string{
	TableWorkflows:                {"id", "name", "slug", "description", "draftGraph", "draftRevision", "publishedVersionId", "createdAt", "updatedAt", "archivedAt", "agentPresentation"},
	TableWorkflowVersions:         {"id", "workflowId", "version", "graph", "inputSchema", "createdAt", "agentPresentation"},
	TableRuns:                     {"id", "workflowId", "workflowVersionId", "draftRevision", "graphSnapshot", "mode", "status", "input", "output", "error", "startedAt", "endedAt", "sourceRunId", "sourceNodeId", "retryOfRunId", "retryKey", "inputRedactedPaths", "cancelRequestedAt", "heartbeatAt", "agentRequestKey"},
	TableNodeRuns:                 {"id", "runId", "nodeId", "nodeType", "status", "input", "output", "error", "startedAt", "endedAt"},
	TableRunEvents:                {"runId", "sequence", "type", "nodeId", "status", "input", "output", "activePorts", "error", "inputRedactedPaths", "outputRedactedPaths", "dataBytes", "timestamp"},
	TableWorkflowDraftCheckpoints: {"workflowId", "sourceRevision", "restoredRevision", "graph", "agentPresentation", "restoredFromVersionId", "createdAt"},
}

func decodeRecord[T any](raw json.RawMessage) (T, error) {
	var result T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, Wrap(CodeArchiveInvalid, "decode backup record fields", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return result, Wrap(CodeArchiveInvalid, "validate backup record boundary", err)
	}
	return result, nil
}

func decodeWorkflowRecord(raw json.RawMessage) (WorkflowRecord, error) {
	if err := requireRecordFields(TableWorkflows, raw); err != nil {
		return WorkflowRecord{}, err
	}
	record, err := decodeRecord[WorkflowRecord](raw)
	if err != nil {
		return WorkflowRecord{}, err
	}
	if !validUUID(record.ID) || record.Name == "" || record.Slug == "" || record.DraftRevision <= 0 ||
		!validOptionalUUID(record.PublishedVersionID) || !validUTC(record.CreatedAt) || !validUTC(record.UpdatedAt) ||
		!validOptionalUTC(record.ArchivedAt) || !jsonObject(record.DraftGraph) || !jsonObject(record.AgentPresentation) {
		return WorkflowRecord{}, invalidRecord()
	}
	return record, nil
}

func decodeWorkflowVersionRecord(raw json.RawMessage) (WorkflowVersionRecord, error) {
	if err := requireRecordFields(TableWorkflowVersions, raw); err != nil {
		return WorkflowVersionRecord{}, err
	}
	record, err := decodeRecord[WorkflowVersionRecord](raw)
	if err != nil {
		return WorkflowVersionRecord{}, err
	}
	if !validUUID(record.ID) || !validUUID(record.WorkflowID) || record.Version <= 0 || !validUTC(record.CreatedAt) ||
		!jsonObject(record.Graph) || !jsonObject(record.InputSchema) || !jsonObject(record.AgentPresentation) {
		return WorkflowVersionRecord{}, invalidRecord()
	}
	return record, nil
}

func decodeRunRecord(raw json.RawMessage) (RunRecord, error) {
	if err := requireRecordFields(TableRuns, raw); err != nil {
		return RunRecord{}, err
	}
	record, err := decodeRecord[RunRecord](raw)
	if err != nil {
		return RunRecord{}, err
	}
	if !validUUID(record.ID) || !validUUID(record.WorkflowID) || !validOptionalUUID(record.WorkflowVersionID) ||
		!validOptionalUUID(record.SourceRunID) || !validOptionalUUID(record.RetryOfRunID) || !validOptionalUUID(record.RetryKey) ||
		!validOptionalUUID(record.AgentRequestKey) || (record.DraftRevision != nil && *record.DraftRevision <= 0) ||
		!oneOf(record.Mode, "test", "published", "debug") || !oneOf(record.Status, "running", "cancelling", "completed", "failed", "cancelled") ||
		!jsonObject(record.Input) || (record.GraphSnapshot != nil && !jsonObject(*record.GraphSnapshot)) ||
		(record.Error != nil && !jsonObject(*record.Error)) || record.InputRedactedPaths == nil || !validUTC(record.StartedAt) ||
		!validOptionalUTC(record.EndedAt) || !validOptionalUTC(record.CancelRequestedAt) || !validOptionalUTC(record.HeartbeatAt) ||
		!validRunSourceFields(record) || (record.RetryOfRunID == nil) != (record.RetryKey == nil) ||
		(record.AgentRequestKey != nil && record.Mode != "published") {
		return RunRecord{}, invalidRecord()
	}
	return record, nil
}

func validRunSourceFields(record RunRecord) bool {
	switch record.Mode {
	case "published":
		return record.WorkflowVersionID != nil && record.DraftRevision == nil && record.GraphSnapshot == nil &&
			record.SourceRunID == nil && record.SourceNodeID == nil
	case "test":
		return record.WorkflowVersionID == nil && record.DraftRevision != nil && record.GraphSnapshot != nil &&
			record.SourceRunID == nil && record.SourceNodeID == nil
	case "debug":
		return record.WorkflowVersionID == nil && record.DraftRevision == nil && record.GraphSnapshot != nil &&
			record.SourceRunID != nil && record.SourceNodeID != nil && *record.SourceNodeID != ""
	default:
		return false
	}
}

func decodeNodeRunRecord(raw json.RawMessage) (NodeRunRecord, error) {
	if err := requireRecordFields(TableNodeRuns, raw); err != nil {
		return NodeRunRecord{}, err
	}
	record, err := decodeRecord[NodeRunRecord](raw)
	if err != nil {
		return NodeRunRecord{}, err
	}
	if !validUUID(record.ID) || !validUUID(record.RunID) || record.NodeID == "" || record.NodeType == "" ||
		!nodeStatus(record.Status) || (record.Error != nil && !jsonObject(*record.Error)) ||
		!validOptionalUTC(record.StartedAt) || !validOptionalUTC(record.EndedAt) {
		return NodeRunRecord{}, invalidRecord()
	}
	return record, nil
}

func decodeRunEventRecord(raw json.RawMessage) (RunEventRecord, error) {
	if err := requireRecordFields(TableRunEvents, raw); err != nil {
		return RunEventRecord{}, err
	}
	record, err := decodeRecord[RunEventRecord](raw)
	if err != nil {
		return RunEventRecord{}, err
	}
	if !validUUID(record.RunID) || record.Sequence <= 0 || !oneOf(record.Type,
		"run.started", "node.started", "node.completed", "node.failed", "node.skipped", "node.cancelled", "run.completed", "run.failed", "run.cancelled") ||
		(record.Status != nil && !nodeStatus(*record.Status)) || (record.Error != nil && !jsonObject(*record.Error)) ||
		record.ActivePorts == nil || record.InputRedactedPaths == nil || record.OutputRedactedPaths == nil ||
		record.DataBytes < 0 || !validUTC(record.Timestamp) {
		return RunEventRecord{}, invalidRecord()
	}
	return record, nil
}

func decodeWorkflowDraftCheckpointRecord(raw json.RawMessage) (WorkflowDraftCheckpointRecord, error) {
	if err := requireRecordFields(TableWorkflowDraftCheckpoints, raw); err != nil {
		return WorkflowDraftCheckpointRecord{}, err
	}
	record, err := decodeRecord[WorkflowDraftCheckpointRecord](raw)
	if err != nil {
		return WorkflowDraftCheckpointRecord{}, err
	}
	if !validUUID(record.WorkflowID) || !validUUID(record.RestoredFromVersionID) || record.SourceRevision <= 0 ||
		record.RestoredRevision != record.SourceRevision+1 || !jsonObject(record.Graph) ||
		!jsonObject(record.AgentPresentation) || !validUTC(record.CreatedAt) {
		return WorkflowDraftCheckpointRecord{}, invalidRecord()
	}
	return record, nil
}

func validateTableRecord(name TableName, raw json.RawMessage) error {
	switch name {
	case TableWorkflows:
		_, err := decodeWorkflowRecord(raw)
		return err
	case TableWorkflowVersions:
		_, err := decodeWorkflowVersionRecord(raw)
		return err
	case TableRuns:
		_, err := decodeRunRecord(raw)
		return err
	case TableNodeRuns:
		_, err := decodeNodeRunRecord(raw)
		return err
	case TableRunEvents:
		_, err := decodeRunEventRecord(raw)
		return err
	case TableWorkflowDraftCheckpoints:
		_, err := decodeWorkflowDraftCheckpointRecord(raw)
		return err
	default:
		return Wrap(CodeArchiveInvalid, "select backup record type", nil)
	}
}

func requireRecordFields(name TableName, raw json.RawMessage) error {
	want, ok := recordFields[name]
	if !ok {
		return invalidRecord()
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil || len(fields) != len(want) {
		return Wrap(CodeArchiveInvalid, "validate backup record fields", err)
	}
	for _, field := range want {
		if _, exists := fields[field]; !exists {
			return invalidRecord()
		}
	}
	return nil
}

func validUUID(value string) bool { _, err := uuid.Parse(value); return err == nil }

func validOptionalUUID(value *string) bool { return value == nil || validUUID(*value) }

func validUTC(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}

func validOptionalUTC(value *time.Time) bool { return value == nil || validUTC(*value) }

func jsonObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func nodeStatus(value string) bool {
	return oneOf(value, "pending", "running", "completed", "failed", "skipped", "cancelled")
}

func invalidRecord() error { return Wrap(CodeArchiveInvalid, "validate backup record", nil) }
