CREATE INDEX idx_comment_user_statistics
    ON comment (deleted_at, fk_user_id, fk_action_id, created_at);
