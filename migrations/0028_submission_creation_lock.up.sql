-- One transaction-owned mutex per uploader, not a quota counter/reservation.
-- Keep this separate from discord_user: locking that parent exclusively can
-- block unrelated foreign-key writes inside the receiver's process mutex.
CREATE TABLE submission_creation_lock
(
    fk_user_id BIGINT NOT NULL PRIMARY KEY,
    CONSTRAINT fk_submission_creation_lock_user FOREIGN KEY (fk_user_id)
        REFERENCES discord_user (id) ON DELETE CASCADE
) ENGINE=InnoDB;
