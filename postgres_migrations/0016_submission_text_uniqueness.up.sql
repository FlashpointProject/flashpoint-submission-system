-- Preserve MariaDB text uniqueness without changing stored spelling, IDs, or
-- any existing launcher PostgreSQL table. Comparison keys include the source
-- collation's case/accent and PAD SPACE behavior, frozen by migration 15.
-- Building these indexes fails on collisions: import preflight must report them,
-- never silently discard rows. NULL name values retain ordinary UNIQUE semantics.

CREATE UNIQUE INDEX idx_submission_level_name_source_unique ON submission_level (submission_text_key(name));
ALTER TABLE submission_level DROP CONSTRAINT submission_level_name_key;

CREATE UNIQUE INDEX idx_action_name_source_unique ON action (submission_text_key(name));
ALTER TABLE action DROP CONSTRAINT action_name_key;

CREATE UNIQUE INDEX idx_submission_notification_type_name_source_unique ON submission_notification_type (submission_text_key(name));
ALTER TABLE submission_notification_type DROP CONSTRAINT submission_notification_type_name_key;

CREATE UNIQUE INDEX idx_curation_image_type_name_source_unique ON curation_image_type (submission_text_key(name));
ALTER TABLE curation_image_type DROP CONSTRAINT curation_image_type_name_key;

CREATE UNIQUE INDEX idx_submission_file_current_filename_source_unique ON submission_file (submission_text_key(current_filename));
ALTER TABLE submission_file DROP CONSTRAINT submission_file_current_filename_key;

CREATE UNIQUE INDEX idx_submission_file_md5sum_source_unique ON submission_file (submission_text_key(md5sum));
ALTER TABLE submission_file DROP CONSTRAINT submission_file_md5sum_key;

CREATE UNIQUE INDEX idx_submission_file_sha256sum_source_unique ON submission_file (submission_text_key(sha256sum));
ALTER TABLE submission_file DROP CONSTRAINT submission_file_sha256sum_key;

CREATE UNIQUE INDEX idx_curation_image_filename_source_unique ON curation_image (submission_text_key(filename));
ALTER TABLE curation_image DROP CONSTRAINT curation_image_filename_key;

CREATE UNIQUE INDEX idx_masterdb_game_uuid_source_unique ON masterdb_game (submission_text_key(uuid));
ALTER TABLE masterdb_game DROP CONSTRAINT masterdb_game_uuid_key;

-- OAuth was created under general_ci, unlike the other submission text keys.
-- Keep its raw primary key for schema identity; source-equivalent duplicates
-- are additionally excluded by this index. The DAL targets this expression in
-- ON CONFLICT, so differently spelled equivalents update the original row.
CREATE UNIQUE INDEX idx_oauth_client_id_source_unique
    ON oauth_client (submission_general_text_key(client_id));
