package backup

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
)

// NullableJSONB is the v1alpha1 wire representation for nullable PostgreSQL
// jsonb columns. Valid=false means SQL NULL; Valid=true preserves Value,
// including a JSON literal null.
type NullableJSONB struct {
	Valid bool            `json:"valid"`
	Value json.RawMessage `json:"value"`
}

func (value NullableJSONB) MarshalJSON() ([]byte, error) {
	raw := value.Value
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	if !json.Valid(raw) || (!value.Valid && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))) {
		return nil, errors.New("invalid nullable jsonb state")
	}
	type wire NullableJSONB
	return json.Marshal(wire{Valid: value.Valid, Value: raw})
}

func (value *NullableJSONB) UnmarshalJSON(body []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil || len(fields) != 2 {
		return errors.New("invalid nullable jsonb wire")
	}
	validRaw, hasValid := fields["valid"]
	valueRaw, hasValue := fields["value"]
	validToken := bytes.TrimSpace(validRaw)
	if !hasValid || !hasValue ||
		(!bytes.Equal(validToken, []byte("true")) && !bytes.Equal(validToken, []byte("false"))) ||
		!json.Valid(valueRaw) {
		return errors.New("invalid nullable jsonb wire")
	}
	valid := bytes.Equal(validToken, []byte("true"))
	if !valid && !bytes.Equal(bytes.TrimSpace(valueRaw), []byte("null")) {
		return errors.New("invalid nullable jsonb state")
	}
	value.Valid = valid
	value.Value = append(value.Value[:0], valueRaw...)
	return nil
}

func nullableJSONB(body []byte) NullableJSONB {
	if len(body) == 0 {
		return NullableJSONB{}
	}
	return NullableJSONB{Valid: true, Value: append(json.RawMessage(nil), body...)}
}

func (value NullableJSONB) databaseValue() any {
	if !value.Valid {
		return nil
	}
	if len(value.Value) == 0 {
		return json.RawMessage("null")
	}
	return value.Value
}

func validNullableJSONB(value NullableJSONB) bool {
	if !value.Valid {
		return len(value.Value) == 0 || bytes.Equal(bytes.TrimSpace(value.Value), []byte("null"))
	}
	return json.Valid(value.Value)
}

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
	ID                 string          `json:"id"`
	WorkflowID         string          `json:"workflowId"`
	WorkflowVersionID  *string         `json:"workflowVersionId"`
	DraftRevision      *int64          `json:"draftRevision"`
	GraphSnapshot      NullableJSONB   `json:"graphSnapshot"`
	Mode               string          `json:"mode"`
	Status             string          `json:"status"`
	Input              json.RawMessage `json:"input"`
	Output             NullableJSONB   `json:"output"`
	Error              NullableJSONB   `json:"error"`
	StartedAt          time.Time       `json:"startedAt"`
	EndedAt            *time.Time      `json:"endedAt"`
	SourceRunID        *string         `json:"sourceRunId"`
	SourceNodeID       *string         `json:"sourceNodeId"`
	RetryOfRunID       *string         `json:"retryOfRunId"`
	RetryKey           *string         `json:"retryKey"`
	InputRedactedPaths []string        `json:"inputRedactedPaths"`
	CancelRequestedAt  *time.Time      `json:"cancelRequestedAt"`
	HeartbeatAt        *time.Time      `json:"heartbeatAt"`
	AgentRequestKey    *string         `json:"agentRequestKey"`
}

type NodeRunRecord struct {
	ID        string        `json:"id"`
	RunID     string        `json:"runId"`
	NodeID    string        `json:"nodeId"`
	NodeType  string        `json:"nodeType"`
	Status    string        `json:"status"`
	Input     NullableJSONB `json:"input"`
	Output    NullableJSONB `json:"output"`
	Error     NullableJSONB `json:"error"`
	StartedAt *time.Time    `json:"startedAt"`
	EndedAt   *time.Time    `json:"endedAt"`
}

type RunEventRecord struct {
	RunID               string        `json:"runId"`
	Sequence            int64         `json:"sequence"`
	Type                string        `json:"type"`
	NodeID              *string       `json:"nodeId"`
	Status              *string       `json:"status"`
	Input               NullableJSONB `json:"input"`
	Output              NullableJSONB `json:"output"`
	ActivePorts         []string      `json:"activePorts"`
	Error               NullableJSONB `json:"error"`
	InputRedactedPaths  []string      `json:"inputRedactedPaths"`
	OutputRedactedPaths []string      `json:"outputRedactedPaths"`
	DataBytes           int64         `json:"dataBytes"`
	Timestamp           time.Time     `json:"timestamp"`
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

type RunRecordV1Alpha2 struct {
	RunRecord
	ExecutionProtocol   int16      `json:"executionProtocol"`
	LeaseToken          int64      `json:"leaseToken"`
	RecoveryReason      *string    `json:"recoveryReason"`
	RecoveryRequestedAt *time.Time `json:"recoveryRequestedAt"`
}

type NodeRunRecordV1Alpha2 struct {
	NodeRunRecord
	Attempt int `json:"attempt"`
}

type RunEventRecordV1Alpha2 struct {
	RunEventRecord
	NodeAttempt *int `json:"nodeAttempt"`
}

type RunPayloadRecord struct {
	RunID             string    `json:"runId"`
	Sequence          int64     `json:"sequence"`
	Kind              string    `json:"kind"`
	NodeID            *string   `json:"nodeId"`
	NodeAttempt       *int      `json:"nodeAttempt"`
	ExecutionProtocol int16     `json:"executionProtocol"`
	CipherVersion     int16     `json:"cipherVersion"`
	Ciphertext        string    `json:"ciphertext"`
	CreatedAt         time.Time `json:"createdAt"`
}

var recordFields = map[TableName][]string{
	TableWorkflows:                {"id", "name", "slug", "description", "draftGraph", "draftRevision", "publishedVersionId", "createdAt", "updatedAt", "archivedAt", "agentPresentation"},
	TableWorkflowVersions:         {"id", "workflowId", "version", "graph", "inputSchema", "createdAt", "agentPresentation"},
	TableRuns:                     {"id", "workflowId", "workflowVersionId", "draftRevision", "graphSnapshot", "mode", "status", "input", "output", "error", "startedAt", "endedAt", "sourceRunId", "sourceNodeId", "retryOfRunId", "retryKey", "inputRedactedPaths", "cancelRequestedAt", "heartbeatAt", "agentRequestKey"},
	TableNodeRuns:                 {"id", "runId", "nodeId", "nodeType", "status", "input", "output", "error", "startedAt", "endedAt"},
	TableRunEvents:                {"runId", "sequence", "type", "nodeId", "status", "input", "output", "activePorts", "error", "inputRedactedPaths", "outputRedactedPaths", "dataBytes", "timestamp"},
	TableWorkflowDraftCheckpoints: {"workflowId", "sourceRevision", "restoredRevision", "graph", "agentPresentation", "restoredFromVersionId", "createdAt"},
}

var recordFieldsV1Alpha2 = map[TableName][]string{
	TableWorkflows:                recordFields[TableWorkflows],
	TableWorkflowVersions:         recordFields[TableWorkflowVersions],
	TableRuns:                     append(append([]string{}, recordFields[TableRuns]...), "executionProtocol", "leaseToken", "recoveryReason", "recoveryRequestedAt"),
	TableNodeRuns:                 append(append([]string{}, recordFields[TableNodeRuns]...), "attempt"),
	TableRunEvents:                append(append([]string{}, recordFields[TableRunEvents]...), "nodeAttempt"),
	TableRunPayloads:              {"runId", "sequence", "kind", "nodeId", "nodeAttempt", "executionProtocol", "cipherVersion", "ciphertext", "createdAt"},
	TableWorkflowDraftCheckpoints: recordFields[TableWorkflowDraftCheckpoints],
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
	if !validRunRecord(record) || !oneOf(record.Status, "running", "cancelling", "completed", "failed", "cancelled") {
		return RunRecord{}, invalidRecord()
	}
	return record, nil
}

func validRunRecord(record RunRecord) bool {
	return validUUID(record.ID) && validUUID(record.WorkflowID) && validOptionalUUID(record.WorkflowVersionID) &&
		validOptionalUUID(record.SourceRunID) && validOptionalUUID(record.RetryOfRunID) && validOptionalUUID(record.RetryKey) &&
		validOptionalUUID(record.AgentRequestKey) && (record.DraftRevision == nil || *record.DraftRevision > 0) &&
		oneOf(record.Mode, "test", "published", "debug") && oneOf(record.Status, "queued", "running", "recovery_required", "cancelling", "completed", "failed", "cancelled") &&
		jsonObject(record.Input) && validNullableJSONB(record.GraphSnapshot) && validNullableJSONB(record.Output) &&
		validNullableJSONB(record.Error) && record.InputRedactedPaths != nil && validUTC(record.StartedAt) &&
		validOptionalUTC(record.EndedAt) && validOptionalUTC(record.CancelRequestedAt) && validOptionalUTC(record.HeartbeatAt)
}

func validRunLocalSemantics(record RunRecord) bool {
	return validRunSourceFields(record) &&
		(record.RetryOfRunID == nil) == (record.RetryKey == nil) &&
		(record.AgentRequestKey == nil || record.Mode == "published")
}

func validRunSourceFields(record RunRecord) bool {
	switch record.Mode {
	case "published":
		return record.WorkflowVersionID != nil && record.DraftRevision == nil && !record.GraphSnapshot.Valid &&
			record.SourceRunID == nil && record.SourceNodeID == nil
	case "test":
		return record.WorkflowVersionID == nil && record.DraftRevision != nil && record.GraphSnapshot.Valid &&
			record.SourceRunID == nil && record.SourceNodeID == nil
	case "debug":
		return record.WorkflowVersionID == nil && record.DraftRevision == nil && record.GraphSnapshot.Valid &&
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
	if !validNodeRunRecord(record) {
		return NodeRunRecord{}, invalidRecord()
	}
	return record, nil
}

func validNodeRunRecord(record NodeRunRecord) bool {
	return validUUID(record.ID) && validUUID(record.RunID) && record.NodeID != "" && record.NodeType != "" &&
		nodeStatus(record.Status) && validNullableJSONB(record.Input) && validNullableJSONB(record.Output) &&
		validNullableJSONB(record.Error) && validOptionalUTC(record.StartedAt) && validOptionalUTC(record.EndedAt)
}

func decodeRunEventRecord(raw json.RawMessage) (RunEventRecord, error) {
	if err := requireRecordFields(TableRunEvents, raw); err != nil {
		return RunEventRecord{}, err
	}
	record, err := decodeRecord[RunEventRecord](raw)
	if err != nil {
		return RunEventRecord{}, err
	}
	if !validRunEventRecord(record) || !oneOf(record.Type,
		"run.started", "node.started", "node.completed", "node.failed", "node.skipped", "node.cancelled", "run.completed", "run.failed", "run.cancelled") {
		return RunEventRecord{}, invalidRecord()
	}
	return record, nil
}

func validRunEventRecord(record RunEventRecord) bool {
	return validUUID(record.RunID) && record.Sequence > 0 && oneOf(record.Type,
		"run.queued", "run.started", "run.recovery_required", "node.started", "node.completed", "node.failed", "node.skipped", "node.cancelled", "node.retry_confirmed", "run.completed", "run.failed", "run.cancelled") &&
		(record.Status == nil || nodeStatus(*record.Status)) && validNullableJSONB(record.Input) &&
		validNullableJSONB(record.Output) && validNullableJSONB(record.Error) && record.ActivePorts != nil &&
		record.InputRedactedPaths != nil && record.OutputRedactedPaths != nil && record.DataBytes >= 0 && validUTC(record.Timestamp)
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

func decodeRunRecordV1Alpha2(raw json.RawMessage) (RunRecordV1Alpha2, error) {
	if err := requireRecordFieldsForVersion(APIVersionV1Alpha2, TableRuns, raw); err != nil {
		return RunRecordV1Alpha2{}, err
	}
	record, err := decodeRecord[RunRecordV1Alpha2](raw)
	if err != nil || !validRunRecord(record.RunRecord) || record.ExecutionProtocol < 0 || record.LeaseToken < 0 ||
		!validOptionalUTC(record.RecoveryRequestedAt) || !validRecoveryFields(record.Status, record.RecoveryReason, record.RecoveryRequestedAt) {
		return RunRecordV1Alpha2{}, invalidRecord()
	}
	return record, nil
}

func decodeNodeRunRecordV1Alpha2(raw json.RawMessage) (NodeRunRecordV1Alpha2, error) {
	if err := requireRecordFieldsForVersion(APIVersionV1Alpha2, TableNodeRuns, raw); err != nil {
		return NodeRunRecordV1Alpha2{}, err
	}
	record, err := decodeRecord[NodeRunRecordV1Alpha2](raw)
	if err != nil || record.Attempt < 1 || record.Attempt > 3 || !validNodeRunRecord(record.NodeRunRecord) {
		return NodeRunRecordV1Alpha2{}, invalidRecord()
	}
	return record, nil
}

func decodeRunEventRecordV1Alpha2(raw json.RawMessage) (RunEventRecordV1Alpha2, error) {
	if err := requireRecordFieldsForVersion(APIVersionV1Alpha2, TableRunEvents, raw); err != nil {
		return RunEventRecordV1Alpha2{}, err
	}
	record, err := decodeRecord[RunEventRecordV1Alpha2](raw)
	if err != nil || !validRunEventRecord(record.RunEventRecord) || !validEventAttempt(record.Type, record.NodeID, record.NodeAttempt) {
		return RunEventRecordV1Alpha2{}, invalidRecord()
	}
	return record, nil
}

func decodeRunPayloadRecord(raw json.RawMessage) (RunPayloadRecord, error) {
	if err := requireRecordFieldsForVersion(APIVersionV1Alpha2, TableRunPayloads, raw); err != nil {
		return RunPayloadRecord{}, err
	}
	record, err := decodeRecord[RunPayloadRecord](raw)
	if err != nil || !validUUID(record.RunID) || record.ExecutionProtocol <= 0 || record.CipherVersion <= 0 || !validUTC(record.CreatedAt) ||
		!validPayloadMetadata(record) {
		return RunPayloadRecord{}, invalidRecord()
	}
	ciphertext, err := base64.StdEncoding.Strict().DecodeString(record.Ciphertext)
	if err != nil || len(ciphertext) == 0 || len(ciphertext) > MaxRecordBytes || base64.StdEncoding.EncodeToString(ciphertext) != record.Ciphertext {
		return RunPayloadRecord{}, invalidRecord()
	}
	return record, nil
}

func validRecoveryFields(status string, reason *string, requestedAt *time.Time) bool {
	if status == "recovery_required" {
		return reason != nil && requestedAt != nil && oneOf(*reason, "legacy_active_run", "uncertain_read_only", "uncertain_side_effect", "attempt_limit_reached", "payload_unavailable", "event_history_invalid", "node_version_unavailable")
	}
	return reason == nil && requestedAt == nil
}

func validPayloadMetadata(record RunPayloadRecord) bool {
	switch record.Kind {
	case "run_input":
		return record.Sequence == 0 && record.NodeID == nil && record.NodeAttempt == nil
	case "node_input", "node_output":
		return record.Sequence > 0 && record.NodeID != nil && *record.NodeID != "" && record.NodeAttempt != nil && *record.NodeAttempt >= 1 && *record.NodeAttempt <= 3
	default:
		return false
	}
}

func validEventAttempt(eventType string, nodeID *string, attempt *int) bool {
	if len(eventType) >= 4 && eventType[:4] == "run." {
		return nodeID == nil && attempt == nil
	}
	return nodeID != nil && *nodeID != "" && attempt != nil && *attempt >= 1 && *attempt <= 3
}

func validateTableRecord(name TableName, raw json.RawMessage) error {
	return validateTableRecordForVersion(APIVersionV1Alpha1, name, raw)
}

func validateTableRecordForVersion(version string, name TableName, raw json.RawMessage) error {
	if version == APIVersionV1Alpha2 {
		switch name {
		case TableRuns:
			_, err := decodeRunRecordV1Alpha2(raw)
			return err
		case TableNodeRuns:
			_, err := decodeNodeRunRecordV1Alpha2(raw)
			return err
		case TableRunEvents:
			_, err := decodeRunEventRecordV1Alpha2(raw)
			return err
		case TableRunPayloads:
			_, err := decodeRunPayloadRecord(raw)
			return err
		}
	}
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
	return requireRecordFieldsForVersion(APIVersionV1Alpha1, name, raw)
}

func requireRecordFieldsForVersion(version string, name TableName, raw json.RawMessage) error {
	fieldSet := recordFields
	if version == APIVersionV1Alpha2 {
		fieldSet = recordFieldsV1Alpha2
	}
	want, ok := fieldSet[name]
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
