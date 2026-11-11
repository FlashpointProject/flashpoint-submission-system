-- The curation validator looks up every submission's launch command.
-- Hash indexes: equality only, and no length limit (unlike btree).
CREATE INDEX IF NOT EXISTS "IDX_game_launch_command" ON "game" USING hash ("launch_command");
CREATE INDEX IF NOT EXISTS "IDX_game_data_launch_command" ON "game_data" USING hash ("launch_command");
