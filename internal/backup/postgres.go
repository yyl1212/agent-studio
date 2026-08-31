package backup

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const runExportColumns = `r.id::text,r.workflow_id::text,r.workflow_version_id::text,r.draft_revision,
	r.graph_snapshot,r.mode,r.status,r.input,r.output,r.error,r.started_at,r.ended_at,
	r.source_run_id::text,r.source_node_id,r.retry_of_run_id::text,r.retry_key::text,
	r.input_redacted_paths,r.cancel_requested_at,r.heartbeat_at,r.agent_request_key::text`

type postgresExporter func(context.Context, pgx.Tx, io.Writer) (uint64, error)

func postgresTableWriters(_ context.Context, transaction pgx.Tx) (map[TableName]TableWriter, error) {
	exporters := map[TableName]postgresExporter{
		TableWorkflows: exportWorkflows, TableWorkflowVersions: exportWorkflowVersions,
		TableRuns: exportRuns, TableNodeRuns: exportNodeRuns, TableRunEvents: exportRunEvents,
		TableWorkflowDraftCheckpoints: exportWorkflowDraftCheckpoints,
	}
	writers := make(map[TableName]TableWriter, len(TableOrder))
	for _, name := range TableOrder {
		name := name
		exporter := exporters[name]
		writers[name] = func(ctx context.Context, destination io.Writer) (TableManifest, error) {
			observed := &exportWriter{destination: destination, hash: sha256.New()}
			records, err := exporter(ctx, transaction, observed)
			if err != nil {
				return TableManifest{}, err
			}
			path, _ := tablePath(name)
			return TableManifest{
				Name: name, Path: path, Records: records, UncompressedBytes: observed.bytes,
				Digest: digestPrefix + hex.EncodeToString(observed.hash.Sum(nil)),
			}, nil
		}
	}
	return writers, nil
}

type exportWriter struct {
	destination io.Writer
	hash        hash.Hash
	bytes       uint64
}

func (writer *exportWriter) Write(body []byte) (int, error) {
	count, err := writer.destination.Write(body)
	if count > 0 {
		_, _ = writer.hash.Write(body[:count])
		writer.bytes += uint64(count)
	}
	return count, err
}

func writeRecord(writer io.Writer, table TableName, record any) error {
	body, err := json.Marshal(record)
	if err != nil {
		return Wrap(CodeCreateFailed, "encode source record", err)
	}
	if err := validateTableRecord(table, body); err != nil {
		return err
	}
	body = append(body, '\n')
	if _, err := writer.Write(body); err != nil {
		return Wrap(CodeCreateFailed, "write source record", err)
	}
	return nil
}

func exportWorkflows(ctx context.Context, transaction pgx.Tx, writer io.Writer) (uint64, error) {
	rows, err := transaction.Query(ctx, `SELECT id::text,name,slug,description,draft_graph,draft_revision,
		published_version_id::text,created_at,updated_at,archived_at,agent_presentation
		FROM workflows ORDER BY id`)
	if err != nil {
		return 0, Wrap(CodeCreateFailed, "query source workflows", err)
	}
	defer rows.Close()
	var count uint64
	for rows.Next() {
		var record WorkflowRecord
		if err := rows.Scan(&record.ID, &record.Name, &record.Slug, &record.Description, &record.DraftGraph,
			&record.DraftRevision, &record.PublishedVersionID, &record.CreatedAt, &record.UpdatedAt,
			&record.ArchivedAt, &record.AgentPresentation); err != nil {
			return 0, Wrap(CodeCreateFailed, "scan source workflow", err)
		}
		normalizeWorkflow(&record)
		if err := writeRecord(writer, TableWorkflows, record); err != nil {
			return 0, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, Wrap(CodeCreateFailed, "iterate source workflows", err)
	}
	return count, nil
}

func exportWorkflowVersions(ctx context.Context, transaction pgx.Tx, writer io.Writer) (uint64, error) {
	rows, err := transaction.Query(ctx, `SELECT id::text,workflow_id::text,version,graph,input_schema,created_at,agent_presentation
		FROM workflow_versions ORDER BY workflow_id,version,id`)
	if err != nil {
		return 0, Wrap(CodeCreateFailed, "query source workflow versions", err)
	}
	defer rows.Close()
	var count uint64
	for rows.Next() {
		var record WorkflowVersionRecord
		if err := rows.Scan(&record.ID, &record.WorkflowID, &record.Version, &record.Graph, &record.InputSchema,
			&record.CreatedAt, &record.AgentPresentation); err != nil {
			return 0, Wrap(CodeCreateFailed, "scan source workflow version", err)
		}
		record.CreatedAt = record.CreatedAt.UTC()
		if err := writeRecord(writer, TableWorkflowVersions, record); err != nil {
			return 0, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, Wrap(CodeCreateFailed, "iterate source workflow versions", err)
	}
	return count, nil
}

type runReference struct {
	ID           string
	SourceRunID  *string
	RetryOfRunID *string
	StartedAt    time.Time
}

func exportRuns(ctx context.Context, transaction pgx.Tx, writer io.Writer) (uint64, error) {
	references, err := loadRunReferences(ctx, transaction)
	if err != nil {
		return 0, err
	}
	ordered, err := orderRuns(references)
	if err != nil {
		return 0, err
	}
	var count uint64
	for start := 0; start < len(ordered); start += 512 {
		end := start + 512
		if end > len(ordered) {
			end = len(ordered)
		}
		ids := make([]uuid.UUID, end-start)
		for index, reference := range ordered[start:end] {
			ids[index] = uuid.MustParse(reference.ID)
		}
		rows, err := transaction.Query(ctx, `SELECT `+runExportColumns+`
			FROM unnest($1::uuid[]) WITH ORDINALITY requested(id,ordinal)
			JOIN runs r ON r.id=requested.id ORDER BY requested.ordinal`, ids)
		if err != nil {
			return 0, Wrap(CodeCreateFailed, "query source run batch", err)
		}
		batchIndex := 0
		for rows.Next() {
			if batchIndex >= len(ids) {
				rows.Close()
				return 0, Wrap(CodeReferenceInvalid, "validate source run batch", nil)
			}
			record, err := scanRunRecord(rows)
			if err != nil {
				rows.Close()
				return 0, err
			}
			if record.ID != ordered[start+batchIndex].ID {
				rows.Close()
				return 0, Wrap(CodeReferenceInvalid, "validate source run order", nil)
			}
			if err := writeRecord(writer, TableRuns, record); err != nil {
				rows.Close()
				return 0, err
			}
			batchIndex++
			count++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, Wrap(CodeCreateFailed, "iterate source run batch", err)
		}
		rows.Close()
		if batchIndex != len(ids) {
			return 0, Wrap(CodeReferenceInvalid, "validate source run batch", nil)
		}
	}
	return count, nil
}

func loadRunReferences(ctx context.Context, transaction pgx.Tx) ([]runReference, error) {
	rows, err := transaction.Query(ctx, `SELECT id::text,source_run_id::text,retry_of_run_id::text,started_at FROM runs`)
	if err != nil {
		return nil, Wrap(CodeCreateFailed, "query source run references", err)
	}
	defer rows.Close()
	references := make([]runReference, 0)
	for rows.Next() {
		var reference runReference
		if err := rows.Scan(&reference.ID, &reference.SourceRunID, &reference.RetryOfRunID, &reference.StartedAt); err != nil {
			return nil, Wrap(CodeCreateFailed, "scan source run reference", err)
		}
		reference.StartedAt = reference.StartedAt.UTC()
		references = append(references, reference)
	}
	if err := rows.Err(); err != nil {
		return nil, Wrap(CodeCreateFailed, "iterate source run references", err)
	}
	return references, nil
}

func orderRuns(references []runReference) ([]runReference, error) {
	byID := make(map[string]runReference, len(references))
	indegree := make(map[string]int, len(references))
	children := make(map[string][]string, len(references))
	for _, reference := range references {
		if !validUUID(reference.ID) || !validUTC(reference.StartedAt) {
			return nil, Wrap(CodeReferenceInvalid, "validate source run reference", nil)
		}
		if _, duplicate := byID[reference.ID]; duplicate {
			return nil, Wrap(CodeReferenceInvalid, "validate duplicate source run", nil)
		}
		byID[reference.ID] = reference
		indegree[reference.ID] = 0
	}
	for _, reference := range references {
		parents := make(map[string]bool, 2)
		for _, parent := range []*string{reference.SourceRunID, reference.RetryOfRunID} {
			if parent == nil || parents[*parent] {
				continue
			}
			parents[*parent] = true
			if _, exists := byID[*parent]; !exists {
				return nil, Wrap(CodeReferenceInvalid, "validate source run parent", nil)
			}
			indegree[reference.ID]++
			children[*parent] = append(children[*parent], reference.ID)
		}
	}
	queue := &runReferenceHeap{}
	heap.Init(queue)
	for id, degree := range indegree {
		if degree == 0 {
			heap.Push(queue, byID[id])
		}
	}
	ordered := make([]runReference, 0, len(references))
	for queue.Len() > 0 {
		reference := heap.Pop(queue).(runReference)
		ordered = append(ordered, reference)
		for _, child := range children[reference.ID] {
			indegree[child]--
			if indegree[child] == 0 {
				heap.Push(queue, byID[child])
			}
		}
	}
	if len(ordered) != len(references) {
		return nil, Wrap(CodeReferenceInvalid, "validate source run dependency graph", nil)
	}
	return ordered, nil
}

type runReferenceHeap []runReference

func (items runReferenceHeap) Len() int { return len(items) }
func (items runReferenceHeap) Less(i, j int) bool {
	if items[i].StartedAt.Equal(items[j].StartedAt) {
		return items[i].ID < items[j].ID
	}
	return items[i].StartedAt.Before(items[j].StartedAt)
}
func (items runReferenceHeap) Swap(i, j int)   { items[i], items[j] = items[j], items[i] }
func (items *runReferenceHeap) Push(value any) { *items = append(*items, value.(runReference)) }
func (items *runReferenceHeap) Pop() any {
	old := *items
	last := old[len(old)-1]
	*items = old[:len(old)-1]
	return last
}

func scanRunRecord(row pgx.Row) (RunRecord, error) {
	var record RunRecord
	var graph, input, output, errorJSON []byte
	if err := row.Scan(
		&record.ID, &record.WorkflowID, &record.WorkflowVersionID, &record.DraftRevision,
		&graph, &record.Mode, &record.Status, &input, &output, &errorJSON, &record.StartedAt, &record.EndedAt,
		&record.SourceRunID, &record.SourceNodeID, &record.RetryOfRunID, &record.RetryKey,
		&record.InputRedactedPaths, &record.CancelRequestedAt, &record.HeartbeatAt, &record.AgentRequestKey,
	); err != nil {
		return RunRecord{}, Wrap(CodeCreateFailed, "scan source run", err)
	}
	record.GraphSnapshot = nullableJSONB(graph)
	record.Input = append(json.RawMessage(nil), input...)
	record.Output = nullableJSONB(output)
	record.Error = nullableJSONB(errorJSON)
	record.InputRedactedPaths = nonNilStrings(record.InputRedactedPaths)
	record.StartedAt = record.StartedAt.UTC()
	normalizeOptionalTime(record.EndedAt)
	normalizeOptionalTime(record.CancelRequestedAt)
	normalizeOptionalTime(record.HeartbeatAt)
	return record, nil
}

func exportNodeRuns(ctx context.Context, transaction pgx.Tx, writer io.Writer) (uint64, error) {
	rows, err := transaction.Query(ctx, `SELECT id::text,run_id::text,node_id,node_type,status,input,output,error,started_at,ended_at
		FROM node_runs ORDER BY run_id,node_id,id`)
	if err != nil {
		return 0, Wrap(CodeCreateFailed, "query source node runs", err)
	}
	defer rows.Close()
	var count uint64
	for rows.Next() {
		var record NodeRunRecord
		var input, output, errorJSON []byte
		if err := rows.Scan(&record.ID, &record.RunID, &record.NodeID, &record.NodeType, &record.Status,
			&input, &output, &errorJSON, &record.StartedAt, &record.EndedAt); err != nil {
			return 0, Wrap(CodeCreateFailed, "scan source node run", err)
		}
		record.Input, record.Output, record.Error = nullableJSONB(input), nullableJSONB(output), nullableJSONB(errorJSON)
		normalizeOptionalTime(record.StartedAt)
		normalizeOptionalTime(record.EndedAt)
		if err := writeRecord(writer, TableNodeRuns, record); err != nil {
			return 0, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, Wrap(CodeCreateFailed, "iterate source node runs", err)
	}
	return count, nil
}

func exportRunEvents(ctx context.Context, transaction pgx.Tx, writer io.Writer) (uint64, error) {
	rows, err := transaction.Query(ctx, `SELECT run_id::text,sequence,type,node_id,status,input,output,active_ports,error,
		input_redacted_paths,output_redacted_paths,data_bytes,timestamp FROM run_events ORDER BY run_id,sequence`)
	if err != nil {
		return 0, Wrap(CodeCreateFailed, "query source run events", err)
	}
	defer rows.Close()
	var count uint64
	for rows.Next() {
		var record RunEventRecord
		var input, output, errorJSON []byte
		if err := rows.Scan(&record.RunID, &record.Sequence, &record.Type, &record.NodeID, &record.Status,
			&input, &output, &record.ActivePorts, &errorJSON, &record.InputRedactedPaths,
			&record.OutputRedactedPaths, &record.DataBytes, &record.Timestamp); err != nil {
			return 0, Wrap(CodeCreateFailed, "scan source run event", err)
		}
		record.Input, record.Output, record.Error = nullableJSONB(input), nullableJSONB(output), nullableJSONB(errorJSON)
		record.ActivePorts = nonNilStrings(record.ActivePorts)
		record.InputRedactedPaths = nonNilStrings(record.InputRedactedPaths)
		record.OutputRedactedPaths = nonNilStrings(record.OutputRedactedPaths)
		record.Timestamp = record.Timestamp.UTC()
		if err := writeRecord(writer, TableRunEvents, record); err != nil {
			return 0, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, Wrap(CodeCreateFailed, "iterate source run events", err)
	}
	return count, nil
}

func exportWorkflowDraftCheckpoints(ctx context.Context, transaction pgx.Tx, writer io.Writer) (uint64, error) {
	rows, err := transaction.Query(ctx, `SELECT workflow_id::text,source_revision,restored_revision,graph,
		agent_presentation,restored_from_version_id::text,created_at
		FROM workflow_draft_checkpoints ORDER BY workflow_id`)
	if err != nil {
		return 0, Wrap(CodeCreateFailed, "query source workflow checkpoints", err)
	}
	defer rows.Close()
	var count uint64
	for rows.Next() {
		var record WorkflowDraftCheckpointRecord
		if err := rows.Scan(&record.WorkflowID, &record.SourceRevision, &record.RestoredRevision, &record.Graph,
			&record.AgentPresentation, &record.RestoredFromVersionID, &record.CreatedAt); err != nil {
			return 0, Wrap(CodeCreateFailed, "scan source workflow checkpoint", err)
		}
		record.CreatedAt = record.CreatedAt.UTC()
		if err := writeRecord(writer, TableWorkflowDraftCheckpoints, record); err != nil {
			return 0, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, Wrap(CodeCreateFailed, "iterate source workflow checkpoints", err)
	}
	return count, nil
}

func normalizeWorkflow(record *WorkflowRecord) {
	record.CreatedAt = record.CreatedAt.UTC()
	record.UpdatedAt = record.UpdatedAt.UTC()
	normalizeOptionalTime(record.ArchivedAt)
}

func normalizeOptionalTime(value *time.Time) {
	if value != nil {
		*value = value.UTC()
	}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
