CREATE TABLE task_comment (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    task_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    content VARCHAR(1000) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_task_comment_task
        FOREIGN KEY (workspace_id, task_id)
        REFERENCES task (workspace_id, id)
        ON DELETE CASCADE
);

CREATE INDEX idx_task_comment_workspace_task_id
    ON task_comment (workspace_id, task_id, id ASC);
CREATE INDEX idx_task_comment_user_id ON task_comment (user_id);
