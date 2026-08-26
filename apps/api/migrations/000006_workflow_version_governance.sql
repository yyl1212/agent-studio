CREATE TABLE workflow_draft_checkpoints (
  workflow_id uuid PRIMARY KEY REFERENCES workflows(id) ON DELETE CASCADE,
  source_revision bigint NOT NULL CHECK (source_revision > 0),
  restored_revision bigint NOT NULL CHECK (restored_revision = source_revision + 1),
  graph jsonb NOT NULL CHECK (jsonb_typeof(graph) = 'object'),
  agent_presentation jsonb NOT NULL CHECK (jsonb_typeof(agent_presentation) = 'object'),
  restored_from_version_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT workflow_draft_checkpoints_version_fk
    FOREIGN KEY (workflow_id, restored_from_version_id)
    REFERENCES workflow_versions(workflow_id, id)
);
