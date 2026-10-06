package integration_tests

import ("database/sql";"github.com/FlashpointProject/flashpoint-submission-system/config")

// Fixture SQL uses the production MariaDB dialect.
func testSQL(query string) string { return query }
func openSubmissionTestDB(conf *config.Config) (*sql.DB, error) { return openMariaTestDB(conf) }
