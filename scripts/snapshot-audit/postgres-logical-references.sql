-- Bound shared-memory use in small default Docker /dev/shm environments.
SET max_parallel_workers_per_gather=0;
-- These relationships have no declared FK. Changelogs are intentionally excluded:
-- a historic row may legitimately outlive its current entity. Count only; no IDs.
SELECT 'tag_alias.tag_id -> tag.id' AS relationship, COUNT(*) AS missing_parent_rows FROM tag_alias c WHERE NOT EXISTS(SELECT 1 FROM tag p WHERE p.id=c.tag_id)
UNION ALL SELECT 'tag.primary_alias -> tag_alias.name', COUNT(*) FROM tag c WHERE NOT EXISTS(SELECT 1 FROM tag_alias p WHERE p.name=c.primary_alias)
UNION ALL SELECT 'tag.primary_alias -> same tag_alias.tag_id', COUNT(*) FROM tag c JOIN tag_alias p ON p.name=c.primary_alias WHERE p.tag_id<>c.id
UNION ALL SELECT 'platform.primary_alias -> same platform_alias.platform_id', COUNT(*) FROM platform c JOIN platform_alias p ON p.name=c.primary_alias WHERE p.platform_id<>c.id
UNION ALL SELECT 'platform_alias.platform_id -> platform.id', COUNT(*) FROM platform_alias c WHERE NOT EXISTS(SELECT 1 FROM platform p WHERE p.id=c.platform_id)
UNION ALL SELECT 'game_tags_tag.game_id -> game.id', COUNT(*) FROM game_tags_tag c WHERE NOT EXISTS(SELECT 1 FROM game p WHERE p.id=c.game_id)
UNION ALL SELECT 'game_tags_tag.tag_id -> tag.id', COUNT(*) FROM game_tags_tag c WHERE NOT EXISTS(SELECT 1 FROM tag p WHERE p.id=c.tag_id)
UNION ALL SELECT 'game_platforms_platform.game_id -> game.id', COUNT(*) FROM game_platforms_platform c WHERE NOT EXISTS(SELECT 1 FROM game p WHERE p.id=c.game_id)
UNION ALL SELECT 'game_platforms_platform.platform_id -> platform.id', COUNT(*) FROM game_platforms_platform c WHERE NOT EXISTS(SELECT 1 FROM platform p WHERE p.id=c.platform_id)
UNION ALL SELECT 'additional_app.parent_game_id -> game.id', COUNT(*) FROM additional_app c WHERE NOT EXISTS(SELECT 1 FROM game p WHERE p.id=c.parent_game_id)
UNION ALL SELECT 'game_data.game_id -> game.id', COUNT(*) FROM game_data c WHERE c.game_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game p WHERE p.id=c.game_id)
UNION ALL SELECT 'game.active_data_id -> game_data.id', COUNT(*) FROM game c WHERE c.active_data_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game_data p WHERE p.id=c.active_data_id)
UNION ALL SELECT 'game.active_data_id -> same game_data.game_id', COUNT(*) FROM game c JOIN game_data p ON p.id=c.active_data_id WHERE p.game_id IS DISTINCT FROM c.id
UNION ALL SELECT 'game.parent_game_id(nonempty) -> game.id', COUNT(*) FROM game c WHERE NULLIF(c.parent_game_id,'') IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game p WHERE p.id=c.parent_game_id)
UNION ALL SELECT 'game_data_index.game_id -> game.id', COUNT(*) FROM game_data_index c WHERE c.game_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM game p WHERE p.id=c.game_id::text)
ORDER BY 1;
