ALTER TABLE game DROP COLUMN logo_path;
ALTER TABLE game DROP COLUMN screenshot_path;
ALTER TABLE changelog_game DROP COLUMN logo_path;
ALTER TABLE changelog_game DROP COLUMN screenshot_path;

CREATE OR REPLACE FUNCTION log_game()
    RETURNS TRIGGER AS $$
BEGIN
CALL log_game_data_for_game(NEW.id, NEW.date_modified);
CALL log_add_app_for_game(NEW.id, NEW.date_modified);
CALL log_platform_relations('game_id', NEW.id, NEW.date_modified);
CALL log_tag_relations('game_id', NEW.id, NEW.date_modified);

INSERT INTO changelog_game (id, parent_game_id, title, alternate_titles, series, developer,
                            publisher, date_added, date_modified, play_mode, status, notes, source,
                            application_path, launch_command, release_date, version, original_description,
                            language, library, active_data_id, tags_str, platforms_str, action, reason,
                            user_id, platform_name, archive_state, ruffle_support)
VALUES (NEW.id, NEW.parent_game_id, NEW.title, NEW.alternate_titles, NEW.series, NEW.developer,
        NEW.publisher, NEW.date_added, NEW.date_modified, NEW.play_mode, NEW.status, NEW.notes, NEW.source,
        NEW.application_path, NEW.launch_command, NEW.release_date, NEW.version, NEW.original_description,
        NEW.language, NEW.library, NEW.active_data_id, NEW.tags_str, NEW.platforms_str, NEW.action, NEW.reason,
        NEW.user_id, NEW.platform_name, NEW.archive_state, NEW.ruffle_support);

RETURN NEW;
END;
$$ LANGUAGE plpgsql;