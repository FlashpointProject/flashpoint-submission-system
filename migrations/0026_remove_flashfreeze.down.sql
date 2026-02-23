CREATE TABLE flashfreeze_file
(
    id                BIGINT PRIMARY KEY AUTO_INCREMENT,
    fk_user_id        BIGINT              NOT NULL,
    original_filename VARCHAR(255)        NOT NULL,
    current_filename  VARCHAR(255) UNIQUE NOT NULL,
    size              BIGINT              NOT NULL,
    created_at        BIGINT              NOT NULL,
    md5sum            CHAR(32) UNIQUE     NOT NULL,
    sha256sum         CHAR(64) UNIQUE     NOT NULL,
    indexed_at        BIGINT       DEFAULT NULL,
    deleted_at        BIGINT       DEFAULT NULL,
    deleted_reason    VARCHAR(255) DEFAULT NULL,
    indexing_errors   BIGINT       DEFAULT NULL,
    FULLTEXT (original_filename) WITH PARSER NGRAM,
    FOREIGN KEY (fk_user_id) REFERENCES discord_user (id)
);
CREATE INDEX idx_flashfreeze_file_created_at ON flashfreeze_file (created_at);
CREATE INDEX idx_flashfreeze_file_deleted_at ON flashfreeze_file (deleted_at);

CREATE TABLE flashfreeze_file_contents
(
    id                     BIGINT PRIMARY KEY AUTO_INCREMENT,
    fk_flashfreeze_file_id BIGINT   NOT NULL,
    filename               TEXT     NOT NULL,
    size_compressed        BIGINT   NOT NULL,
    size_uncompressed      BIGINT   NOT NULL,
    md5sum                 CHAR(32) NOT NULL,
    sha256sum              CHAR(64) NOT NULL,
    description            TEXT     NOT NULL,
    FULLTEXT (filename) WITH PARSER NGRAM,
    FULLTEXT (description) WITH PARSER NGRAM,
    FOREIGN KEY (fk_flashfreeze_file_id) REFERENCES flashfreeze_file (id)
);
CREATE INDEX idx_flashfreeze_file_contents_size_compressed ON flashfreeze_file_contents (size_compressed);
CREATE INDEX idx_flashfreeze_file_contents_size_uncompressed ON flashfreeze_file_contents (size_uncompressed);
CREATE INDEX idx_flashfreeze_file_contents_size_md5sum ON flashfreeze_file_contents (md5sum);
CREATE INDEX idx_flashfreeze_file_contents_size_sha256sum ON flashfreeze_file_contents (sha256sum);
CREATE INDEX idx_flashfreeze_file_contents_filename_prefix ON flashfreeze_file_contents (filename(700));
CREATE INDEX idx_flashfreeze_file_contents_description_prefix ON flashfreeze_file_contents (description(700));
