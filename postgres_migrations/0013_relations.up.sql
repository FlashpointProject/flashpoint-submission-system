CREATE OR REPLACE PROCEDURE log_tag_relations(id_column VARCHAR(40), value anyelement, date_modified timestamp)
AS $$
BEGIN
	EXECUTE format('INSERT INTO changelog_game_tags_tag ("game_id", "tag_id", "date_modified") ' ||
				   'SELECT game_id, tag_id, $2 FROM game_tags_tag ' ||
				   'WHERE %I = $1', id_column) USING value, date_modified;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE PROCEDURE log_platform_relations(id_column VARCHAR(40), value anyelement, date_modified timestamp)
AS $$
BEGIN
	EXECUTE format('INSERT INTO changelog_game_platforms_platform ("game_id", "platform_id", "date_modified") ' ||
				   'SELECT game_id, platform_id, $2 FROM game_platforms_platform ' ||
				   'WHERE %I = $1', id_column) USING value, date_modified;
END;
$$ LANGUAGE plpgsql;