ALTER TABLE runs DROP CONSTRAINT runs_mode_check;
ALTER TABLE runs DROP CONSTRAINT runs_mode_source_check;

ALTER TABLE runs
  ADD COLUMN source_run_id uuid NULL,
  ADD COLUMN source_node_id text NULL,
  ADD CONSTRAINT runs_id_workflow_unique UNIQUE (id, workflow_id),
  ADD CONSTRAINT runs_source_workflow_fk
    FOREIGN KEY (source_run_id, workflow_id) REFERENCES runs(id, workflow_id),
  ADD CONSTRAINT runs_mode_check CHECK (mode IN ('test','published','debug')),
  ADD CONSTRAINT runs_mode_source_check CHECK (
    (mode='published' AND workflow_version_id IS NOT NULL AND draft_revision IS NULL AND graph_snapshot IS NULL AND source_run_id IS NULL AND source_node_id IS NULL)
    OR
    (mode='test' AND workflow_version_id IS NULL AND draft_revision IS NOT NULL AND graph_snapshot IS NOT NULL AND source_run_id IS NULL AND source_node_id IS NULL)
    OR
    (mode='debug' AND workflow_version_id IS NULL AND draft_revision IS NULL AND graph_snapshot IS NOT NULL AND source_run_id IS NOT NULL AND source_node_id IS NOT NULL)
  );

CREATE TABLE run_events (
  run_id uuid NOT NULL REFERENCES runs(id),
  sequence bigint NOT NULL CHECK (sequence > 0),
  type text NOT NULL CHECK (type IN (
    'run.started','node.started','node.completed','node.failed','node.skipped',
    'node.cancelled','run.completed','run.failed','run.cancelled'
  )),
  node_id text NULL,
  status text NULL,
  input jsonb NULL,
  output jsonb NULL,
  active_ports text[] NOT NULL DEFAULT '{}'::text[],
  error jsonb NULL,
  input_redacted_paths text[] NOT NULL DEFAULT '{}'::text[],
  output_redacted_paths text[] NOT NULL DEFAULT '{}'::text[],
  data_bytes bigint NOT NULL CHECK (data_bytes >= 0),
  timestamp timestamptz NOT NULL,
  PRIMARY KEY (run_id, sequence)
);
