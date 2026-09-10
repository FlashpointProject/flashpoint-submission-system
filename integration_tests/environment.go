package integration_tests

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
)

var testDatabaseName = regexp.MustCompile(`^fpfss_test_[a-z0-9]{8,32}$`)

// Each top-level setup owns its env and writable files. t.Setenv/t.Chdir also
// reject t.Parallel: existing nested scenarios intentionally share DB fixtures.
func setupTestEnvironment(t *testing.T) string {
	t.Helper()
	name := os.Getenv("FPFSS_TEST_DB_NAME")
	engine := os.Getenv("SUBMISSION_DB_ENGINE")
	if engine == "" {
		engine = "mariadb"
	}
	require.Contains(t, []string{"mariadb", "postgres"}, engine)
	t.Setenv("SUBMISSION_DB_ENGINE", engine)
	require.True(t, testDatabaseName.MatchString(name), "run database tests with bash integration_tests/run.sh; refusing unmanaged databases")
	fixtureDir, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Dir(fixtureDir)
	values, err := godotenv.Read(filepath.Join(fixtureDir, "testenv.env"))
	require.NoError(t, err)
	workDir := t.TempDir()
	for key, value := range values {
		if strings.HasPrefix(value, "./") {
			value = filepath.Join(workDir, value)
			require.NoError(t, os.MkdirAll(value, 0o755))
		}
		t.Setenv(key, value)
	}
	// Only the runner-generated database name is configurable. No developer
	// connection settings or production .env are inherited.
	t.Setenv("DB_IP", "database")
	t.Setenv("DB_PORT", "3306")
	t.Setenv("DB_NAME", name)
	t.Setenv("POSTGRES_HOST", "postgres")
	t.Setenv("POSTGRES_PORT", "5432")
	t.Setenv("POSTGRES_USER", name) // Production PG connector uses user as DB name.
	for _, entry := range []struct{ name, source string }{
		{"templates", filepath.Join(repoRoot, "templates")},
		{"test_files", filepath.Join(fixtureDir, "test_files")},
	} {
		require.NoError(t, os.Symlink(entry.source, filepath.Join(workDir, entry.name)))
	}
	t.Chdir(workDir)
	return repoRoot
}

func validateTestConfig(conf *config.Config, name string) error {
	if !testDatabaseName.MatchString(name) || conf.DBName != name || conf.PostgresUser != name ||
		conf.DBIP != "database" || conf.DBPort != 3306 || conf.DBUser != "fpfss" ||
		conf.PostgresHost != "postgres" || conf.PostgresPort != 5432 {
		return fmt.Errorf("refusing reset outside the isolated Compose test environment; use integration_tests/run.sh")
	}
	return nil
}

func migrationHead(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var head int64
	seen := make(map[int64]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		prefix, _, found := strings.Cut(entry.Name(), "_")
		version, err := strconv.ParseInt(prefix, 10, 64)
		if !found || err != nil || version < 1 || seen[version] {
			return 0, fmt.Errorf("invalid or duplicate migration version: %s", entry.Name())
		}
		seen[version] = true
		if version > head {
			head = version
		}
	}
	if head == 0 {
		return 0, fmt.Errorf("no up migrations in %s", dir)
	}
	return head, nil
}

func validateMigrationState(engine string, version, expected int64, dirty bool, count int) error {
	if count != 1 || dirty || version != expected {
		return fmt.Errorf("%s migration state: version=%d expected=%d dirty=%v rows=%d; refusing reset", engine, version, expected, dirty, count)
	}
	return nil
}

// Check BOTH identities and migration histories before deleting anything.
func verifyTestDatabases(conf *config.Config, repoRoot string) error {
	if err := validateTestConfig(conf, os.Getenv("FPFSS_TEST_DB_NAME")); err != nil {
		return err
	}
	mariaHead, err := migrationHead(filepath.Join(repoRoot, "migrations"))
	if err != nil {
		return err
	}
	pgHead, err := migrationHead(filepath.Join(repoRoot, "postgres_migrations"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	maria, err := openMariaTestDB(conf)
	if err != nil {
		return err
	}
	defer maria.Close()
	var name string
	if err := maria.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&name); err != nil {
		return err
	}
	if name != conf.DBName {
		return fmt.Errorf("unexpected MariaDB database %q", name)
	}
	if err := verifyMariaTestCollations(ctx, maria); err != nil {
		return err
	}
	var version int64
	var dirty bool
	var count int
	if err := maria.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		return err
	}
	if err := maria.QueryRowContext(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		return err
	}
	if err := validateMigrationState("MariaDB", version, mariaHead, dirty, count); err != nil {
		return err
	}
	pg, err := openPostgresTestDB(ctx, conf)
	if err != nil {
		return err
	}
	defer pg.Close()
	if err := pg.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil {
		return err
	}
	if name != conf.PostgresUser {
		return fmt.Errorf("unexpected PostgreSQL database %q", name)
	}
	if err := pg.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		return err
	}
	if err := pg.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		return err
	}
	return validateMigrationState("PostgreSQL", version, pgHead, dirty, count)
}

func prepareTestDatabases(conf *config.Config, repoRoot string) error {
	if err := verifyTestDatabases(conf, repoRoot); err != nil {
		return err
	}
	if err := clearExistingMariaTestDB(conf); err != nil {
		return err
	}
	return clearExistingPostgresTestDB(conf)
}

func resetTestDatabases(t *testing.T, repoRoot string) {
	t.Helper()
	require.NoError(t, prepareTestDatabases(config.GetConfig(nil), repoRoot))
}

func openMariaTestDB(conf *config.Config) (*sql.DB, error) {
	return sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?multiStatements=true&parseTime=true&loc=UTC&time_zone=%%27%%2B00%%3A00%%27", conf.DBUser, conf.DBPassword, conf.DBIP, conf.DBPort, conf.DBName))
}

func openPostgresTestDB(ctx context.Context, conf *config.Config) (*pgxpool.Pool, error) {
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", conf.PostgresUser, conf.PostgresPassword, conf.PostgresHost, conf.PostgresPort, conf.PostgresUser)
	return pgxpool.New(ctx, connStr)
}

func quoteMySQLIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func quotePostgresIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

var preservedMySQLTables = map[string]struct{}{
	"action":                       {},
	"curation_image_type":          {},
	"schema_migrations":            {},
	"submission_level":             {},
	"submission_notification_type": {},
}

var mysqlTableCleanupQueries = map[string]string{
	"discord_user": fmt.Sprintf(
		"DELETE FROM discord_user WHERE id NOT IN (%d, %d)",
		constants.ValidatorID,
		constants.SystemID,
	),
}

func clearExistingMariaTestDB(conf *config.Config) (resultErr error) {
	db, err := openMariaTestDB(conf)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return err
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT TABLE_NAME
		FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA = DATABASE()
			AND TABLE_TYPE = 'BASE TABLE'
		ORDER BY TABLE_NAME`)
	if err != nil {
		return err
	}
	defer rows.Close()

	tables := make([]string, 0)
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return err
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(tables) == 0 {
		return fmt.Errorf("mysql database has no reusable tables")
	}

	if _, err := conn.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS = 0`); err != nil {
		return err
	}
	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := conn.ExecContext(restoreCtx, `SET FOREIGN_KEY_CHECKS = 1`)
		resultErr = errors.Join(resultErr, err)
	}()

	for _, table := range tables {
		if cleanupQuery, ok := mysqlTableCleanupQueries[table]; ok {
			if _, err := conn.ExecContext(ctx, cleanupQuery); err != nil {
				return err
			}
			continue
		}
		if _, ok := preservedMySQLTables[table]; ok {
			continue
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s", quoteMySQLIdentifier(table))); err != nil {
			return err
		}
	}

	return nil
}

func clearExistingPostgresTestDB(conf *config.Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := openPostgresTestDB(ctx, conf)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return err
	}

	rows, err := pool.Query(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = 'public'
			AND table_type = 'BASE TABLE'
			AND table_name NOT IN ('schema_migrations','action','curation_image_type','submission_level','submission_notification_type')
		ORDER BY table_name`)
	if err != nil {
		return err
	}
	defer rows.Close()

	tables := make([]string, 0)
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return err
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if len(tables) == 0 {
		return fmt.Errorf("postgres database has no reusable tables")
	}

	qualifiedTables := make([]string, 0, len(tables))
	for _, table := range tables {
		qualifiedTables = append(qualifiedTables, quotePostgresIdentifier("public")+"."+quotePostgresIdentifier(table))
	}

	_, err = pool.Exec(ctx, fmt.Sprintf("TRUNCATE TABLE %s RESTART IDENTITY CASCADE", strings.Join(qualifiedTables, ", ")))
	if err != nil {
		return err
	}
	// Restoring only the two built-in users avoids preserving stale user state.
	var hasUsers bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass('public.discord_user') IS NOT NULL").Scan(&hasUsers); err != nil {
		return err
	}
	if hasUsers {
		_, err = pool.Exec(ctx, `INSERT INTO discord_user(id,username,avatar,discriminator,public_flags,flags,locale,mfa_enabled) VALUES ($1,'RedMinima','156dd40e0c72ed8e84034b53aad32af4','1337',0,0,'en_US',false),($2,'FPFSS','43989404743f92a70f293df092a59034','1337',0,0,'en_US',false)`, constants.ValidatorID, constants.SystemID)
	}
	return err
}
