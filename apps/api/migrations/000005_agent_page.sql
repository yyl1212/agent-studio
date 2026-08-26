ALTER TABLE workflows ADD COLUMN agent_presentation jsonb;
ALTER TABLE workflow_versions ADD COLUMN agent_presentation jsonb;
ALTER TABLE runs ADD COLUMN agent_request_key uuid NULL;

UPDATE workflows
SET agent_presentation = jsonb_build_object(
  'title', name,
  'description', description,
  'accent', 'indigo',
  'submitLabel', '运行 Agent',
  'resultMode', 'auto'
);

UPDATE workflow_versions v
SET agent_presentation = jsonb_build_object(
  'title', w.name,
  'description', w.description,
  'accent', 'indigo',
  'submitLabel', '运行 Agent',
  'resultMode', 'auto'
)
FROM workflows w
WHERE w.id = v.workflow_id;

ALTER TABLE workflows
  ALTER COLUMN agent_presentation SET NOT NULL,
  ADD CONSTRAINT workflows_agent_presentation_object_check
    CHECK (jsonb_typeof(agent_presentation) = 'object');

ALTER TABLE workflow_versions
  ALTER COLUMN agent_presentation SET NOT NULL,
  ADD CONSTRAINT workflow_versions_agent_presentation_object_check
    CHECK (jsonb_typeof(agent_presentation) = 'object');

ALTER TABLE runs
  ADD CONSTRAINT runs_agent_request_key_mode_check
    CHECK (agent_request_key IS NULL OR mode = 'published');

CREATE UNIQUE INDEX runs_agent_request_key_unique_idx
  ON runs(workflow_id, agent_request_key)
  WHERE agent_request_key IS NOT NULL AND mode = 'published';
