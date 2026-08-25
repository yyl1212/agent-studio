ALTER TABLE workflows
  ADD COLUMN archived_at timestamptz NULL;

CREATE INDEX workflows_management_idx
  ON workflows(archived_at, updated_at DESC, id DESC);

CREATE INDEX runs_started_at_id_idx
  ON runs(started_at DESC, id DESC);

CREATE INDEX runs_workflow_started_at_id_idx
  ON runs(workflow_id, started_at DESC, id DESC);

CREATE INDEX runs_status_started_at_id_idx
  ON runs(status, started_at DESC, id DESC);

CREATE INDEX runs_mode_started_at_id_idx
  ON runs(mode, started_at DESC, id DESC);

CREATE INDEX runs_workflow_status_started_at_id_idx
  ON runs(workflow_id, status, started_at DESC, id DESC);

CREATE INDEX runs_workflow_mode_started_at_id_idx
  ON runs(workflow_id, mode, started_at DESC, id DESC);
