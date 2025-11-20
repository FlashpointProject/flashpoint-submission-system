-- Make sure all ID sequencers are ahead of any tag / game / platform already existing
BEGIN;
SELECT SETVAL('public.changelog_platform_id_seq', COALESCE(MAX(id), 1) ) FROM public.changelog_platform;
SELECT SETVAL('public.changelog_tag_id_seq', COALESCE(MAX(id), 1) ) FROM public.changelog_tag;
SELECT SETVAL('public.game_data_id_seq', COALESCE(MAX(id), 1) ) FROM public.game_data;
SELECT SETVAL('public.platform_id_seq', COALESCE(MAX(id), 1) ) FROM public.platform;
SELECT SETVAL('public.tag_category_id_seq', COALESCE(MAX(id), 1) ) FROM public.tag_category;
SELECT SETVAL('public.tag_id_seq', COALESCE(MAX(id), 1) ) FROM public.tag;
COMMIT;

-- Update platforms_str and tags_str for all games
BEGIN;
SET session_replication_role = replica;

-- If you didn't set a valid SystemUid before importing, use this
-- UPDATE game SET user_id = 1234;
-- UPDATE tag SET user_id = 1234;
-- UPDATE platform SET user_id = 1234;
-- UPDATE changelog_game SET user_id = 1234;
-- UPDATE changelog_tag SET user_id = 1234;
-- UPDATE changelog_platform SET user_id = 1234;

UPDATE game
SET platforms_str = coalesce(
    (
        SELECT string_agg(
                       (SELECT primary_alias FROM platform WHERE id = p.platform_id), '; '
                   )
        FROM game_platforms_platform p
        WHERE p.game_id = game.id
    ), ''
) WHERE 1=1;

UPDATE changelog_game
SET platforms_str = coalesce(
    (
        SELECT string_agg(
                       (SELECT primary_alias FROM platform WHERE id = p.platform_id), '; '
                   )
        FROM game_platforms_platform p
        WHERE p.game_id = changelog_game.id
    ), ''
) WHERE 1=1;

UPDATE game
SET tags_str = coalesce(
    (
        SELECT string_agg(
                       (SELECT primary_alias FROM tag WHERE id = t.tag_id), '; '
                   )
        FROM game_tags_tag t
        WHERE t.game_id = game.id
    ), ''
) WHERE 1=1;


UPDATE changelog_game
SET tags_str = coalesce(
    (
        SELECT string_agg(
                       (SELECT primary_alias FROM tag WHERE id = t.tag_id), '; '
                   )
        FROM game_tags_tag t
        WHERE t.game_id = changelog_game.id
    ), ''
) WHERE 1=1;

COMMIT;
SET session_replication_role = DEFAULT;