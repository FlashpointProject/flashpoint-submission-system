-- Migrate all BIGINT timestamp columns to DATETIME(6) for microsecond precision.
-- Uses a temp column approach to safely convert existing unix-second values.
SET time_zone = '+00:00';

-- comment.created_at: BIGINT NOT NULL
ALTER TABLE comment ADD COLUMN created_at_new DATETIME(6) NOT NULL DEFAULT NOW(6);
UPDATE comment SET created_at_new = FROM_UNIXTIME(created_at);
ALTER TABLE comment DROP COLUMN created_at;
ALTER TABLE comment CHANGE created_at_new created_at DATETIME(6) NOT NULL;
CREATE INDEX idx_comment_created_at ON comment (created_at);

-- comment.deleted_at: BIGINT DEFAULT NULL
ALTER TABLE comment ADD COLUMN deleted_at_new DATETIME(6) NULL DEFAULT NULL;
UPDATE comment SET deleted_at_new = FROM_UNIXTIME(deleted_at) WHERE deleted_at IS NOT NULL;
ALTER TABLE comment DROP COLUMN deleted_at;
ALTER TABLE comment CHANGE deleted_at_new deleted_at DATETIME(6) NULL DEFAULT NULL;
CREATE INDEX idx_comment_deleted_at ON comment (deleted_at);

-- submission_file.created_at: BIGINT NOT NULL
ALTER TABLE submission_file ADD COLUMN created_at_new DATETIME(6) NOT NULL DEFAULT NOW(6);
UPDATE submission_file SET created_at_new = FROM_UNIXTIME(created_at);
ALTER TABLE submission_file DROP COLUMN created_at;
ALTER TABLE submission_file CHANGE created_at_new created_at DATETIME(6) NOT NULL;
CREATE INDEX idx_submission_file_created_at ON submission_file (created_at);

-- submission_file.deleted_at: BIGINT DEFAULT NULL
ALTER TABLE submission_file ADD COLUMN deleted_at_new DATETIME(6) NULL DEFAULT NULL;
UPDATE submission_file SET deleted_at_new = FROM_UNIXTIME(deleted_at) WHERE deleted_at IS NOT NULL;
ALTER TABLE submission_file DROP COLUMN deleted_at;
ALTER TABLE submission_file CHANGE deleted_at_new deleted_at DATETIME(6) NULL DEFAULT NULL;
CREATE INDEX idx_submission_file_deleted_at ON submission_file (deleted_at);

-- submission.deleted_at: BIGINT DEFAULT NULL
ALTER TABLE submission ADD COLUMN deleted_at_new DATETIME(6) NULL DEFAULT NULL;
UPDATE submission SET deleted_at_new = FROM_UNIXTIME(deleted_at) WHERE deleted_at IS NOT NULL;
ALTER TABLE submission DROP COLUMN deleted_at;
ALTER TABLE submission CHANGE deleted_at_new deleted_at DATETIME(6) NULL DEFAULT NULL;
CREATE INDEX idx_submission_deleted_at ON submission (deleted_at);

-- submission.frozen_at: BIGINT DEFAULT NULL
ALTER TABLE submission ADD COLUMN frozen_at_new DATETIME(6) NULL DEFAULT NULL;
UPDATE submission SET frozen_at_new = FROM_UNIXTIME(frozen_at) WHERE frozen_at IS NOT NULL;
ALTER TABLE submission DROP COLUMN frozen_at;
ALTER TABLE submission CHANGE frozen_at_new frozen_at DATETIME(6) NULL DEFAULT NULL;

-- submission_notification.created_at: BIGINT NOT NULL
ALTER TABLE submission_notification ADD COLUMN created_at_new DATETIME(6) NOT NULL DEFAULT NOW(6);
UPDATE submission_notification SET created_at_new = FROM_UNIXTIME(created_at);
ALTER TABLE submission_notification DROP COLUMN created_at;
ALTER TABLE submission_notification CHANGE created_at_new created_at DATETIME(6) NOT NULL;
CREATE INDEX idx_submission_notification_created_at ON submission_notification (created_at);

-- submission_notification.sent_at: BIGINT DEFAULT NULL
ALTER TABLE submission_notification ADD COLUMN sent_at_new DATETIME(6) NULL DEFAULT NULL;
UPDATE submission_notification SET sent_at_new = FROM_UNIXTIME(sent_at) WHERE sent_at IS NOT NULL;
ALTER TABLE submission_notification DROP COLUMN sent_at;
ALTER TABLE submission_notification CHANGE sent_at_new sent_at DATETIME(6) NULL DEFAULT NULL;
CREATE INDEX idx_submission_notification_sent_at ON submission_notification (sent_at);

-- submission_notification_subscription.created_at: BIGINT NOT NULL
ALTER TABLE submission_notification_subscription ADD COLUMN created_at_new DATETIME(6) NOT NULL DEFAULT NOW(6);
UPDATE submission_notification_subscription SET created_at_new = FROM_UNIXTIME(created_at);
ALTER TABLE submission_notification_subscription DROP COLUMN created_at;
ALTER TABLE submission_notification_subscription CHANGE created_at_new created_at DATETIME(6) NOT NULL;
CREATE INDEX idx_submission_notification_subscription_created_at ON submission_notification_subscription (created_at);

-- masterdb_game.date_added: BIGINT NULL
ALTER TABLE masterdb_game ADD COLUMN date_added_new DATETIME(6) NULL DEFAULT NULL;
UPDATE masterdb_game SET date_added_new = FROM_UNIXTIME(date_added) WHERE date_added IS NOT NULL;
ALTER TABLE masterdb_game DROP COLUMN date_added;
ALTER TABLE masterdb_game CHANGE date_added_new date_added DATETIME(6) NULL DEFAULT NULL;
CREATE INDEX idx_masterdb_game_date_added ON masterdb_game (date_added);

-- masterdb_game.date_modified: BIGINT NULL
ALTER TABLE masterdb_game ADD COLUMN date_modified_new DATETIME(6) NULL DEFAULT NULL;
UPDATE masterdb_game SET date_modified_new = FROM_UNIXTIME(date_modified) WHERE date_modified IS NOT NULL;
ALTER TABLE masterdb_game DROP COLUMN date_modified;
ALTER TABLE masterdb_game CHANGE date_modified_new date_modified DATETIME(6) NULL DEFAULT NULL;
CREATE INDEX idx_masterdb_game_date_modified ON masterdb_game (date_modified);

-- session.expires_at: BIGINT NOT NULL
ALTER TABLE session ADD COLUMN expires_at_new DATETIME(6) NOT NULL DEFAULT NOW(6);
UPDATE session SET expires_at_new = FROM_UNIXTIME(expires_at);
ALTER TABLE session DROP COLUMN expires_at;
ALTER TABLE session CHANGE expires_at_new expires_at DATETIME(6) NOT NULL;
