-- Read-only probes. Run on the isolated MariaDB snapshot after timing jobs finish.
-- SET NAMES matches the Go driver's default utf8mb4_general_ci connection.
SET NAMES utf8mb4 COLLATE utf8mb4_general_ci;
START TRANSACTION READ ONLY;
SELECT @@character_set_connection, @@collation_connection, @@sql_mode;
SELECT COLLATION(CONCAT(id)) AS numeric_identity_collation,
       COERCIBILITY(CONCAT(id)) AS numeric_identity_coercibility,
       CHARSET(CONCAT(id)) AS numeric_identity_charset
FROM submission LIMIT 1;
SELECT COLLATION(uuid) AS legacy_identity_collation,
       COERCIBILITY(uuid) AS legacy_identity_coercibility,
       CHARSET(uuid) AS legacy_identity_charset,
       COLLATION(title) AS title_collation
FROM masterdb_game LIMIT 1;
SELECT COLLATION(identity) AS union_identity_collation,
       COERCIBILITY(identity) AS union_identity_coercibility,
       CHARSET(identity) AS union_identity_charset
FROM ((SELECT CONCAT(id) AS identity FROM submission LIMIT 1)
      UNION (SELECT uuid FROM masterdb_game LIMIT 1)) candidates LIMIT 1;
-- No application row contents are selected. The following fixtures are CTEs.
WITH live AS (SELECT CAST(10 AS SIGNED) AS id,
                     _utf8mb4'Café' COLLATE utf8mb4_unicode_ci AS title,
                     _utf8mb4'Run ' COLLATE utf8mb4_unicode_ci AS launch_command),
     legacy AS (SELECT _utf8mb4'１０' COLLATE utf8mb4_unicode_ci AS uuid,
                       _utf8mb4'CAFE' COLLATE utf8mb4_unicode_ci AS title,
                       _utf8mb4'run' COLLATE utf8mb4_unicode_ci AS launch_command)
SELECT HEX(identity) AS identity_hex, COLLATION(identity) AS identity_collation,
       HEX(title) AS title_hex, COLLATION(title) AS title_collation,
       HEX(launch_command) AS command_hex, COLLATION(launch_command) AS command_collation
FROM (SELECT CONCAT(id) AS identity,title,launch_command FROM live
      UNION SELECT uuid,title,launch_command FROM legacy) candidates;
WITH live AS (SELECT CAST(10 AS SIGNED) AS id,
                     _utf8mb4'Café' COLLATE utf8mb4_unicode_ci AS title,
                     _utf8mb4'Run ' COLLATE utf8mb4_unicode_ci AS launch_command),
     legacy AS (SELECT _utf8mb4'10' COLLATE utf8mb4_unicode_ci AS uuid,
                       _utf8mb4'CAFE' COLLATE utf8mb4_unicode_ci AS title,
                       _utf8mb4'run' COLLATE utf8mb4_unicode_ci AS launch_command)
SELECT HEX(identity) AS identity_hex, COLLATION(identity) AS identity_collation,
       HEX(title) AS title_hex, COLLATION(title) AS title_collation,
       HEX(launch_command) AS command_hex, COLLATION(launch_command) AS command_collation
FROM (SELECT CONCAT(id) AS identity,title,launch_command FROM live
      UNION SELECT uuid,title,launch_command FROM legacy) candidates;
-- Numeric expression collation versus explicit source-key equivalence.
SELECT CONCAT(CAST(10 AS SIGNED)) = _utf8mb4'１０' COLLATE utf8mb4_unicode_ci AS expression_equal,
       _utf8mb4'10' COLLATE utf8mb4_unicode_ci = _utf8mb4'１０' COLLATE utf8mb4_unicode_ci AS unicode_equal;
ROLLBACK;
