ALTER TABLE runs DROP CONSTRAINT runs_status_check;
ALTER TABLE run_events DROP CONSTRAINT run_events_type_check;

ALTER TABLE runs
  ADD COLUMN execution_protocol smallint NOT NULL DEFAULT 0,
  ADD COLUMN lease_owner text NULL,
  ADD COLUMN lease_token bigint NOT NULL DEFAULT 0,
  ADD COLUMN lease_expires_at timestamptz NULL,
  ADD COLUMN recovery_reason text NULL,
  ADD COLUMN recovery_requested_at timestamptz NULL;

ALTER TABLE run_events
  ADD COLUMN node_attempt integer NULL;

ALTER TABLE node_runs
  ADD COLUMN attempt integer NOT NULL DEFAULT 1;

UPDATE run_events
SET node_attempt = 1
WHERE node_id IS NOT NULL;

UPDATE runs
SET status = 'recovery_required',
    recovery_reason = 'legacy_active_run',
    recovery_requested_at = clock_timestamp(),
    heartbeat_at = NULL
WHERE status = 'running';

ALTER TABLE runs
  ADD CONSTRAINT runs_status_check
    CHECK (status IN ('queued','running','recovery_required','cancelling','completed','failed','cancelled')),
  ADD CONSTRAINT runs_execution_protocol_check
    CHECK (execution_protocol >= 0),
  ADD CONSTRAINT runs_lease_token_check
    CHECK (lease_token >= 0),
  ADD CONSTRAINT runs_lease_pair_check
    CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL)),
  ADD CONSTRAINT runs_lease_status_check
    CHECK (lease_owner IS NULL OR status IN ('running','cancelling')),
  ADD CONSTRAINT runs_recovery_fields_check
    CHECK (
      (status = 'recovery_required' AND recovery_reason IS NOT NULL AND recovery_requested_at IS NOT NULL)
      OR
      (status <> 'recovery_required' AND recovery_reason IS NULL AND recovery_requested_at IS NULL)
    ),
  ADD CONSTRAINT runs_recovery_reason_check
    CHECK (recovery_reason IS NULL OR recovery_reason IN (
      'legacy_active_run','uncertain_read_only','uncertain_side_effect',
      'attempt_limit_reached','payload_unavailable','event_history_invalid',
      'node_version_unavailable'
    ));

ALTER TABLE run_events
  ADD CONSTRAINT run_events_type_check CHECK (type IN (
    'run.queued','run.started','run.recovery_required',
    'node.started','node.completed','node.failed','node.skipped','node.cancelled',
    'node.retry_confirmed','run.completed','run.failed','run.cancelled'
  )),
  ADD CONSTRAINT run_events_node_attempt_check CHECK (
    (type LIKE 'run.%' AND node_id IS NULL AND node_attempt IS NULL)
    OR
    (type LIKE 'node.%' AND node_id IS NOT NULL AND node_id <> '' AND node_attempt BETWEEN 1 AND 3)
  );

ALTER TABLE node_runs
  ADD CONSTRAINT node_runs_attempt_check CHECK (attempt BETWEEN 1 AND 3);

CREATE TABLE run_payloads (
  run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  sequence bigint NOT NULL,
  kind text NOT NULL,
  node_id text NULL,
  node_attempt integer NULL,
  execution_protocol smallint NOT NULL,
  cipher_version smallint NOT NULL,
  ciphertext bytea NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (run_id, sequence, kind),
  CONSTRAINT run_payloads_metadata_check CHECK (
    (kind = 'run_input' AND sequence = 0 AND node_id IS NULL AND node_attempt IS NULL)
    OR
    (kind IN ('node_input','node_output') AND sequence > 0 AND node_id IS NOT NULL AND node_id <> '' AND node_attempt BETWEEN 1 AND 3)
  ),
  CONSTRAINT run_payloads_protocol_check CHECK (execution_protocol > 0),
  CONSTRAINT run_payloads_cipher_version_check CHECK (cipher_version > 0),
  CONSTRAINT run_payloads_ciphertext_check CHECK (octet_length(ciphertext) > 0)
);

CREATE INDEX runs_claimable_idx
  ON runs (started_at, id)
  WHERE status IN ('queued','running','cancelling');

CREATE INDEX runs_recovery_required_idx
  ON runs (started_at DESC, id DESC)
  WHERE status = 'recovery_required';

CREATE INDEX runs_lease_owner_idx
  ON runs (lease_owner, lease_expires_at, id)
  WHERE status IN ('running','cancelling') AND lease_owner IS NOT NULL;
