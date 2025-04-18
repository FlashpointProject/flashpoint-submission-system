\set app_path 'FPSoftware\\startXaraPlugin.bat'
\set new_app_path 'FPSoftware\\FlashpointSecurePlayer.exe'
\set prefix 'xara'

--- Paths

SELECT COUNT(*) as rows_that_would_change
FROM game_data
WHERE application_path = :'app_path'
AND launch_command IS NOT NULL
AND launch_command != ''
AND launch_command NOT LIKE :'prefix' || '%';

SELECT COUNT(*) as rows_that_would_change
FROM game
WHERE application_path = :'app_path'
AND launch_command IS NOT NULL
AND launch_command != ''
AND launch_command NOT LIKE :'prefix' || '%';

UPDATE game_data
SET launch_command = concat(:'prefix' || ' ', launch_command)
WHERE application_path = :'app_path'
AND launch_command IS NOT NULL
AND launch_command != ''
AND launch_command NOT LIKE :'prefix' || '%';

UPDATE game
SET reason = 'Update Launch Command',
    user_id = 810112564787675166
WHERE id IN (
    SELECT game_id FROM game_data
    WHERE application_path = :'app_path'
    AND launch_command LIKE :'prefix' || '%'
);

UPDATE game
SET launch_command = concat(:'prefix' || ' ', launch_command),
    reason = 'Update Launch Command',
    user_id = 810112564787675166
WHERE application_path = :'app_path'
AND launch_command IS NOT NULL
AND launch_command != ''
AND launch_command NOT LIKE :'prefix' || '%';

-- App path

SELECT COUNT(*) as rows_that_would_change
FROM game_data
WHERE application_path = :'app_path';

SELECT COUNT(*) as rows_that_would_change
FROM game
WHERE application_path = :'app_path';

UPDATE game_data
SET application_path = :'new_app_path'
WHERE application_path = :'app_path';

UPDATE game
SET reason = 'Update Application Path',
    user_id = 810112564787675166
WHERE id IN (
    SELECT game_id FROM game_data
    WHERE application_path = :'new_app_path'
    AND launch_command LIKE :'prefix' || '%'
);

UPDATE game
SET application_path = :'new_app_path',
    reason = 'Update Application Path',
    user_id = 810112564787675166
WHERE application_path = :'app_path'
AND launch_command LIKE :'prefix' || '%';

--- Platform aliases

\set old_platform 'Babyz Player'
\set new_platform 'Babyz'

SELECT * FROM platform WHERE primary_alias = :'old_platform';

INSERT INTO platform_alias (name, platform_id) 
  VALUES (:'new_platform', (SELECT id FROM platform WHERE primary_alias = :'old_platform'));

UPDATE platform
  SET primary_alias = :'new_platform',
  reason = 'Update Platform Alias',
  user_id = 810112564787675166
  WHERE id = (SELECT id FROM platform WHERE primary_alias = :'old_platform');

UPDATE game
  SET platform_name = :'new_platform',
    reason = 'Update Platform',
    user_id = 810112564787675166
  WHERE platform_name = :'old_platform';

-- Migrate basilisk

WITH updated_game_data AS (
  UPDATE game_data
  SET application_path = E'FPSoftware\\fpnavigator-portable\\FPNavigator.exe'
  WHERE application_path = E'FPSoftware\\Basilisk-Portable\\Basilisk-Portable.exe'
  RETURNING game_id
)
UPDATE game
SET reason = 'Migrate Basilisk to FPNavigator',
    user_id = 810112564787675166
FROM updated_game_data
WHERE game.id = updated_game_data.game_id;
