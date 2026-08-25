ALTER TABLE runs DROP CONSTRAINT runs_status_check;

ALTER TABLE runs
  ADD COLUMN retry_of_run_id uuid NULL,
  ADD COLUMN retry_key uuid NULL,
  ADD COLUMN input_redacted_paths text[] NOT NULL DEFAULT '{}'::text[],
  ADD COLUMN cancel_requested_at timestamptz NULL,
  ADD COLUMN heartbeat_at timestamptz NULL,
  ADD CONSTRAINT runs_status_check
    CHECK (status IN ('running','cancelling','completed','failed','cancelled')),
  ADD CONSTRAINT runs_retry_workflow_fk
    FOREIGN KEY (retry_of_run_id, workflow_id) REFERENCES runs(id, workflow_id),
  ADD CONSTRAINT runs_retry_pair_check
    CHECK ((retry_of_run_id IS NULL) = (retry_key IS NULL));

CREATE UNIQUE INDEX runs_retry_key_unique_idx
  ON runs(retry_of_run_id, retry_key)
  WHERE retry_of_run_id IS NOT NULL;

CREATE INDEX runs_active_heartbeat_idx
  ON runs(heartbeat_at, id)
  WHERE status IN ('running','cancelling');
