package integration_tests

import (
	"context"
	"database/sql"
	"fmt"
)

func sourceColumnCollation(table, column string) string {
	if table == "oauth_client" && (column == "client_id" || column == "client_secret") {
		return "utf8mb4_general_ci"
	}
	if table == "curation_meta" && column == "additional_applications" {
		return "utf8mb4_bin" // MariaDB's JSON alias has a binary text collation.
	}
	return "utf8mb4_unicode_ci"
}

// Production inherited different defaults over its migration history. Refuse
// resets/tests against a fresh schema with silently different text semantics.
func verifyMariaTestCollations(ctx context.Context, db *sql.DB) error {
	var collation string
	if err := db.QueryRowContext(ctx, "SELECT @@collation_database").Scan(&collation); err != nil {
		return err
	}
	if collation != "utf8mb4_unicode_ci" {
		return fmt.Errorf("MariaDB test schema collation=%s expected=utf8mb4_unicode_ci", collation)
	}
	rows, err := db.QueryContext(ctx, `SELECT TABLE_NAME,TABLE_COLLATION FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_TYPE='BASE TABLE' AND TABLE_NAME <> 'schema_migrations'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var table, got string
		if err := rows.Scan(&table, &got); err != nil {
			rows.Close()
			return err
		}
		want := "utf8mb4_unicode_ci"
		if got != want {
			rows.Close()
			return fmt.Errorf("MariaDB table %s collation=%s expected=%s", table, got, want)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, `SELECT TABLE_NAME,COLUMN_NAME,COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND COLLATION_NAME IS NOT NULL AND TABLE_NAME <> 'schema_migrations'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var table, column, got string
		if err := rows.Scan(&table, &column, &got); err != nil {
			return err
		}
		if want := sourceColumnCollation(table, column); got != want {
			return fmt.Errorf("MariaDB column %s.%s collation=%s expected=%s", table, column, got, want)
		}
	}
	return rows.Err()
}
