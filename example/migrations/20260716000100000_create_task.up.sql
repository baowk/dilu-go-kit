CREATE TABLE task (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    title VARCHAR(200) NOT NULL,
    status SMALLINT NOT NULL DEFAULT 1 CHECK (status IN (1, 2)),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (workspace_id, id)
);

CREATE INDEX idx_task_workspace_id_id ON task (workspace_id, id DESC);
