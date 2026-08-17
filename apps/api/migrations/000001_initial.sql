CREATE TABLE workflows (
  id uuid PRIMARY KEY,
  name text NOT NULL,
  slug text NOT NULL UNIQUE,
  description text NOT NULL DEFAULT '',
  draft_graph jsonb NOT NULL,
  draft_revision bigint NOT NULL DEFAULT 1,
  published_version_id uuid NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workflow_versions (
  id uuid PRIMARY KEY,
  workflow_id uuid NOT NULL REFERENCES workflows(id),
  version integer NOT NULL CHECK (version > 0),
  graph jsonb NOT NULL,
  input_schema jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workflow_id, version),
  UNIQUE (workflow_id, id)
);

ALTER TABLE workflows ADD CONSTRAINT workflows_published_version_fk
  FOREIGN KEY (id, published_version_id)
  REFERENCES workflow_versions(workflow_id, id);

CREATE TABLE runs (
  id uuid PRIMARY KEY,
  workflow_id uuid NOT NULL REFERENCES workflows(id),
  workflow_version_id uuid NULL,
  draft_revision bigint NULL,
  graph_snapshot jsonb NULL,
  mode text NOT NULL CHECK (mode IN ('test','published')),
  status text NOT NULL CHECK (status IN ('running','completed','failed','cancelled')),
  input jsonb NOT NULL,
  output jsonb NULL,
  error jsonb NULL,
  started_at timestamptz NOT NULL,
  ended_at timestamptz NULL,
  CONSTRAINT runs_version_fk FOREIGN KEY (workflow_id, workflow_version_id)
    REFERENCES workflow_versions(workflow_id, id),
  CONSTRAINT runs_mode_source_check CHECK (
    (mode='published' AND workflow_version_id IS NOT NULL AND draft_revision IS NULL AND graph_snapshot IS NULL)
    OR
    (mode='test' AND workflow_version_id IS NULL AND draft_revision IS NOT NULL AND graph_snapshot IS NOT NULL)
  )
);

CREATE TABLE node_runs (
  id uuid PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES runs(id),
  node_id text NOT NULL,
  node_type text NOT NULL,
  status text NOT NULL,
  input jsonb NULL,
  output jsonb NULL,
  error jsonb NULL,
  started_at timestamptz NULL,
  ended_at timestamptz NULL,
  UNIQUE (run_id, node_id)
);

CREATE INDEX workflows_updated_at_idx ON workflows(updated_at DESC);
CREATE INDEX workflow_versions_workflow_version_idx ON workflow_versions(workflow_id, version DESC);
CREATE INDEX runs_workflow_started_at_idx ON runs(workflow_id, started_at DESC);
CREATE INDEX node_runs_run_id_idx ON node_runs(run_id);
