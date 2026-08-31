package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	backup "github.com/yyl1212/agent-studio/internal/backup"
)

const (
	workflowID = "00000000-0000-0000-0000-000000001001"
	versionID  = "00000000-0000-0000-0000-000000002001"
	published  = "00000000-0000-0000-0000-000000003001"
	debug      = "00000000-0000-0000-0000-000000003002"
	retry      = "00000000-0000-0000-0000-000000003003"
	retryKey   = "00000000-0000-0000-0000-000000004001"
)

var fixedTime = time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)

func main() {
	if code := run(os.Args[1:]); code != 0 {
		os.Exit(code)
	}
}

func run(args []string) int {
	if len(args) != 1 || (args[0] != "--check" && args[0] != "--write") {
		_, _ = fmt.Fprintln(os.Stderr, "golden archive usage: generate --check | generate --write")
		return 2
	}
	fixture, err := fixturePath()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "golden archive generation failed")
		return 1
	}
	if err := ensureRegularFixtureTarget(fixture, args[0] == "--check"); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "golden archive fixture target is unsafe")
		return 1
	}
	temporaryDirectory, err := os.MkdirTemp(filepath.Dir(fixture), ".v1alpha1-minimal-")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "golden archive generation failed")
		return 1
	}
	defer os.RemoveAll(temporaryDirectory)
	first := filepath.Join(temporaryDirectory, "first.asbak")
	second := filepath.Join(temporaryDirectory, "second.asbak")
	firstBody, secondBody, err := generateTwice(first, second)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "golden archive generation failed")
		return 1
	}
	if !bytes.Equal(firstBody, secondBody) || sha256.Sum256(firstBody) != sha256.Sum256(secondBody) {
		_, _ = fmt.Fprintln(os.Stderr, "golden archive generation is not deterministic")
		return 1
	}
	if args[0] == "--check" {
		committed, readErr := os.ReadFile(fixture)
		if readErr != nil || !bytes.Equal(committed, firstBody) || sha256.Sum256(committed) != sha256.Sum256(firstBody) {
			_, _ = fmt.Fprintln(os.Stderr, "golden archive fixture is out of date")
			return 1
		}
		_, _ = fmt.Fprintln(os.Stdout, "golden archive: verified")
		return 0
	}
	if err := ensureRegularFixtureTarget(fixture, false); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "golden archive fixture target is unsafe")
		return 1
	}
	if err := os.Rename(first, fixture); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "golden archive replacement failed")
		return 1
	}
	_, _ = fmt.Fprintln(os.Stdout, "golden archive: written")
	return 0
}

func fixturePath() (string, error) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("locate generator source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "v1alpha1-minimal.asbak")), nil
}

func ensureRegularFixtureTarget(path string, requireExisting bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && !requireExisting {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("fixture target is not a regular file")
	}
	return nil
}

func generateTwice(first, second string) ([]byte, []byte, error) {
	base := backup.Manifest{
		APIVersion: backup.APIVersion, CreatedAt: fixedTime, RuntimeVersion: "0.5.0-golden",
		DatabaseMigrationVersion: 6, IncludesRuns: true,
	}
	for _, path := range []string{first, second} {
		if _, err := backup.WriteArchive(context.Background(), path, base, tableWriters()); err != nil {
			return nil, nil, err
		}
	}
	firstBody, err := os.ReadFile(first)
	if err != nil {
		return nil, nil, err
	}
	secondBody, err := os.ReadFile(second)
	if err != nil {
		return nil, nil, err
	}
	return firstBody, secondBody, nil
}

func tableWriters() map[backup.TableName]backup.TableWriter {
	completed := fixedTime.Add(time.Second)
	graph := json.RawMessage(`{"nodes":[{"id":"answer","type":"fixture.answer"}]}`)
	presentation := json.RawMessage(`{"title":"Golden Fixture","accent":"indigo","resultMode":"auto"}`)
	inputSchema := json.RawMessage(`{"type":"object","properties":{"fixture":{"type":"string"}}}`)
	publishedID, debugID := published, debug
	version := versionID
	retryKeyID := retryKey
	nodeRecords := []any{
		backup.NodeRunRecord{ID: "00000000-0000-0000-0000-000000005001", RunID: published, NodeID: "answer", NodeType: "fixture.answer", Status: "completed", Input: raw(`{"fixture":"published"}`), Output: raw(`{"fixture":"published"}`), StartedAt: timePointer(fixedTime), EndedAt: timePointer(completed)},
		backup.NodeRunRecord{ID: "00000000-0000-0000-0000-000000005002", RunID: debug, NodeID: "answer", NodeType: "fixture.answer", Status: "completed", Input: raw(`{"fixture":"debug"}`), Output: raw(`{"fixture":"debug"}`), StartedAt: timePointer(fixedTime.Add(2 * time.Second)), EndedAt: timePointer(fixedTime.Add(3 * time.Second))},
		backup.NodeRunRecord{ID: "00000000-0000-0000-0000-000000005003", RunID: retry, NodeID: "answer", NodeType: "fixture.answer", Status: "completed", Input: raw(`{"fixture":"retry"}`), Output: raw(`{"fixture":"retry"}`), StartedAt: timePointer(fixedTime.Add(4 * time.Second)), EndedAt: timePointer(fixedTime.Add(5 * time.Second))},
	}
	records := map[backup.TableName][]any{
		backup.TableWorkflows: {
			backup.WorkflowRecord{ID: workflowID, Name: "Golden v1alpha1 Workflow", Slug: "golden-v1alpha1", Description: "Synthetic compatibility fixture", DraftGraph: graph, DraftRevision: 7, PublishedVersionID: &version, CreatedAt: fixedTime, UpdatedAt: fixedTime, AgentPresentation: presentation},
		},
		backup.TableWorkflowVersions: {
			backup.WorkflowVersionRecord{ID: versionID, WorkflowID: workflowID, Version: 1, Graph: graph, InputSchema: inputSchema, CreatedAt: fixedTime, AgentPresentation: presentation},
		},
		backup.TableRuns: {
			backup.RunRecord{ID: published, WorkflowID: workflowID, WorkflowVersionID: &version, Mode: "published", Status: "completed", Input: json.RawMessage(`{"fixture":"published"}`), Output: raw(`{"fixture":"published"}`), StartedAt: fixedTime, EndedAt: timePointer(completed), InputRedactedPaths: []string{}},
			backup.RunRecord{ID: debug, WorkflowID: workflowID, GraphSnapshot: &graph, Mode: "debug", Status: "completed", Input: json.RawMessage(`{"fixture":"debug"}`), Output: raw(`{"fixture":"debug"}`), StartedAt: fixedTime.Add(2 * time.Second), EndedAt: timePointer(fixedTime.Add(3 * time.Second)), SourceRunID: &publishedID, SourceNodeID: stringPointer("answer"), InputRedactedPaths: []string{}},
			backup.RunRecord{ID: retry, WorkflowID: workflowID, GraphSnapshot: &graph, Mode: "debug", Status: "completed", Input: json.RawMessage(`{"fixture":"retry"}`), Output: raw(`{"fixture":"retry"}`), StartedAt: fixedTime.Add(4 * time.Second), EndedAt: timePointer(fixedTime.Add(5 * time.Second)), SourceRunID: &publishedID, SourceNodeID: stringPointer("answer"), RetryOfRunID: &debugID, RetryKey: &retryKeyID, InputRedactedPaths: []string{}},
		},
		backup.TableNodeRuns: nodeRecords,
		backup.TableRunEvents: {
			event(published, 1, "run.started", fixedTime), event(published, 2, "run.completed", completed),
			event(debug, 1, "run.started", fixedTime.Add(2*time.Second)), event(debug, 2, "run.completed", fixedTime.Add(3*time.Second)),
			event(retry, 1, "run.started", fixedTime.Add(4*time.Second)), event(retry, 2, "run.completed", fixedTime.Add(5*time.Second)),
		},
		backup.TableWorkflowDraftCheckpoints: {
			backup.WorkflowDraftCheckpointRecord{WorkflowID: workflowID, SourceRevision: 7, RestoredRevision: 8, Graph: graph, AgentPresentation: presentation, RestoredFromVersionID: versionID, CreatedAt: fixedTime.Add(6 * time.Second)},
		},
	}
	writers := make(map[backup.TableName]backup.TableWriter, len(backup.TableOrder))
	for _, table := range backup.TableOrder {
		table, rows := table, append([]any(nil), records[table]...)
		writers[table] = func(_ context.Context, destination io.Writer) (backup.TableManifest, error) {
			hash := sha256.New()
			var size uint64
			for _, row := range rows {
				body, err := json.Marshal(row)
				if err != nil {
					return backup.TableManifest{}, err
				}
				body = append(body, '\n')
				written, err := destination.Write(body)
				if err != nil {
					return backup.TableManifest{}, err
				}
				if written != len(body) {
					return backup.TableManifest{}, io.ErrShortWrite
				}
				_, _ = hash.Write(body)
				size += uint64(len(body))
			}
			return backup.TableManifest{Name: table, Path: tablePaths[table], Records: uint64(len(rows)), UncompressedBytes: size, Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil))}, nil
		}
	}
	return writers
}

var tablePaths = map[backup.TableName]string{
	backup.TableWorkflows: "data/workflows.jsonl", backup.TableWorkflowVersions: "data/workflow_versions.jsonl", backup.TableRuns: "data/runs.jsonl",
	backup.TableNodeRuns: "data/node_runs.jsonl", backup.TableRunEvents: "data/run_events.jsonl", backup.TableWorkflowDraftCheckpoints: "data/workflow_draft_checkpoints.jsonl",
}

func event(runID string, sequence int64, kind string, timestamp time.Time) backup.RunEventRecord {
	var status *string
	if kind == "run.completed" {
		status = stringPointer("completed")
	}
	return backup.RunEventRecord{RunID: runID, Sequence: sequence, Type: kind, Status: status, ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, DataBytes: 0, Timestamp: timestamp}
}

func raw(value string) *json.RawMessage {
	result := json.RawMessage(value)
	return &result
}

func timePointer(value time.Time) *time.Time { return &value }
func stringPointer(value string) *string     { return &value }
