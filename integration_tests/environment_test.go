package integration_tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/stretchr/testify/require"
)

func TestHarnessRejectsUnsafeTargets(t *testing.T) {
	const name = "fpfss_test_abcdefgh"
	valid := config.Config{DBName: name, DBUser: "fpfss", DBIP: "database", DBPort: 3306,
		PostgresUser: name, PostgresHost: "postgres", PostgresPort: 5432}
	require.NoError(t, validateTestConfig(&valid, name))
	for _, name := range []string{"", "fpfss", "fpfss_test_", "fpfss_test_../../production", "fpfss_test_abcdefgh;"} {
		require.Error(t, validateTestConfig(&valid, name))
	}
	for _, change := range []struct {
		name  string
		apply func(*config.Config)
	}{
		{"mysql database", func(c *config.Config) { c.DBName = "production" }},
		{"postgres database", func(c *config.Config) { c.PostgresUser = "production" }},
		{"mysql host", func(c *config.Config) { c.DBIP = "localhost" }},
		{"postgres host", func(c *config.Config) { c.PostgresHost = "localhost" }},
		{"mysql port", func(c *config.Config) { c.DBPort = 3307 }},
		{"postgres port", func(c *config.Config) { c.PostgresPort = 5433 }},
	} {
		t.Run(change.name, func(t *testing.T) {
			conf := valid
			change.apply(&conf)
			require.Error(t, validateTestConfig(&conf, name))
		})
	}
}

func TestHarnessMigrationValidation(t *testing.T) {
	dir := t.TempDir()
	_, err := migrationHead(dir)
	require.Error(t, err)
	for _, name := range []string{"0001_init.up.sql", "0027_datetime.up.sql", "0099_future.down.sql"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o600))
	}
	head, err := migrationHead(dir)
	require.NoError(t, err)
	require.EqualValues(t, 27, head)
	require.NoError(t, validateMigrationState("test", head, head, false, 1))
	require.Error(t, validateMigrationState("test", head-1, head, false, 1))
	require.Error(t, validateMigrationState("test", head, head, true, 1))
	require.Error(t, validateMigrationState("test", head, head, false, 2))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0027_duplicate.up.sql"), nil, 0o600))
	_, err = migrationHead(dir)
	require.Error(t, err)
}

func TestHarnessTemporaryEnvironment(t *testing.T) {
	t.Setenv("FPFSS_TEST_DB_NAME", "fpfss_test_abcdefgh")
	t.Setenv("DB_NAME", "developer-database")
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	var firstDir string
	for _, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			setupTestEnvironment(t)
			cwd, err := os.Getwd()
			require.NoError(t, err)
			require.NotEqual(t, originalDir, cwd)
			require.NotEqual(t, firstDir, cwd)
			firstDir = cwd
			require.Equal(t, "fpfss_test_abcdefgh", os.Getenv("DB_NAME"))
			require.DirExists(t, os.Getenv("SUBMISSIONS_DIR_FULL_PATH"))
			require.FileExists(t, "test_files/Warpstar4K.7z")
			info, err := os.Stat("templates")
			require.NoError(t, err)
			require.True(t, info.IsDir())
		})
		require.Equal(t, "developer-database", os.Getenv("DB_NAME"))
		cwd, err := os.Getwd()
		require.NoError(t, err)
		require.Equal(t, originalDir, cwd)
		require.NoDirExists(t, firstDir)
	}
}

func TestHarnessDatabaseResetAndPreflight(t *testing.T) {
	repoRoot := setupTestEnvironment(t)
	conf := config.GetConfig(nil)
	resetTestDatabases(t, repoRoot)
	ctx := context.Background()
	maria, err := openMariaTestDB(conf)
	require.NoError(t, err)
	t.Cleanup(func() { _ = maria.Close() })
	pg, err := openPostgresTestDB(ctx, conf)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	var version, mode, collation, zone string
	require.NoError(t, maria.QueryRow("SELECT VERSION(), @@sql_mode, @@collation_database, @@time_zone").Scan(&version, &mode, &collation, &zone))
	t.Logf("MariaDB version=%s sql_mode=%s collation=%s timezone=%s", version, mode, collation, zone)
	require.Equal(t, "utf8mb4_unicode_ci", collation)
	require.NoError(t, verifyMariaTestCollations(ctx, maria))
	require.NoError(t, pg.QueryRow(ctx, "SELECT version(), current_setting('TimeZone')").Scan(&version, &zone))
	t.Logf("PostgreSQL version=%s timezone=%s", version, zone)

	_, err = maria.Exec("INSERT INTO discord_user(id,username,avatar,discriminator,public_flags,flags,locale,mfa_enabled) VALUES (123456,'reset probe','','',0,0,'',0)")
	require.NoError(t, err)
	_, err = pg.Exec(ctx, "INSERT INTO tag_category (name, color) VALUES ('reset-probe', '#000000')")
	require.NoError(t, err)
	// Collation drift must also be caught before a reset deletes fixture rows.
	_, err = maria.Exec("ALTER TABLE oauth_client MODIFY client_secret TEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = maria.Exec("ALTER TABLE oauth_client MODIFY client_secret TEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL")
	})
	require.ErrorContains(t, prepareTestDatabases(conf, repoRoot), "oauth_client.client_secret collation")
	var retained int
	require.NoError(t, maria.QueryRow("SELECT COUNT(*) FROM discord_user WHERE id=123456").Scan(&retained))
	require.Equal(t, 1, retained)
	_, err = maria.Exec("ALTER TABLE oauth_client MODIFY client_secret TEXT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL")
	require.NoError(t, err)
	// A stale/dirty second database must not erase rows from the first one.
	_, err = pg.Exec(ctx, "UPDATE schema_migrations SET dirty = true")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pg.Exec(ctx, "UPDATE schema_migrations SET dirty = false") })
	require.ErrorContains(t, prepareTestDatabases(conf, repoRoot), "dirty=true")
	var count int
	require.NoError(t, maria.QueryRow("SELECT COUNT(*) FROM discord_user WHERE id=123456").Scan(&count))
	require.Equal(t, 1, count)
	_, err = pg.Exec(ctx, "UPDATE schema_migrations SET dirty = false")
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, prepareTestDatabases(conf, repoRoot))
		require.NoError(t, maria.QueryRow("SELECT COUNT(*) FROM discord_user WHERE id=123456").Scan(&count))
		require.Zero(t, count)
		require.NoError(t, maria.QueryRow("SELECT COUNT(*) FROM discord_user WHERE id IN (?, ?)", constants.ValidatorID, constants.SystemID).Scan(&count))
		require.Equal(t, 2, count)
		require.NoError(t, pg.QueryRow(ctx, "SELECT COUNT(*) FROM tag_category").Scan(&count))
		require.Zero(t, count)
		require.NoError(t, verifyTestDatabases(conf, repoRoot))
	}
}
