package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
)

type postgresSubmissionDAL struct {
	db *sql.DB
}

func NewPostgresSubmissionDAL(conn *sql.DB) *postgresSubmissionDAL {
	return &postgresSubmissionDAL{
		db: conn,
	}
}

type PostgresSubmissionSession struct {
	context     context.Context
	transaction *sql.Tx
}

// NewSession begins a transaction
func (d *postgresSubmissionDAL) NewSession(ctx context.Context) (DBSession, error) {
	// Mutations wait for their parent/user lock before subsequent reads. Read
	// committed ensures those reads see the transaction that released the lock.
	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}

	return &PostgresSubmissionSession{
		context:     ctx,
		transaction: tx,
	}, nil
}

func (dbs *PostgresSubmissionSession) Commit() error {
	return dbs.transaction.Commit()
}

func (dbs *PostgresSubmissionSession) Rollback() error {
	err := dbs.Tx().Rollback()
	if err == sql.ErrTxDone {
		err = nil
	}
	if err != nil {
		utils.LogCtx(dbs.Ctx()).Error(err)
	}
	return err
}

func (dbs *PostgresSubmissionSession) Tx() *sql.Tx {
	return dbs.transaction
}

func (dbs *PostgresSubmissionSession) Ctx() context.Context {
	return dbs.context
}

// StoreSession store session into the DAL with set expiration date
func (d *postgresSubmissionDAL) StoreSession(dbs DBSession, key string, uid int64, durationSeconds int64, scope string, client string, ipAddr string) error {
	expiration := time.Now().UTC().Add(time.Second * time.Duration(durationSeconds))
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `INSERT INTO session (secret, uid, expires_at, scope, client, ip_addr) VALUES (?, ?, ?, ?, ?, ?)`, key, uid, expiration, scope, client, ipAddr)
	return err
}

// DeleteSession deletes specific session
func (d *postgresSubmissionDAL) DeleteSession(dbs DBSession, secret string) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `DELETE FROM session WHERE submission_text_key(secret)=submission_text_key(?)`, secret)
	return err
}

func (d *postgresSubmissionDAL) GetSessions(dbs DBSession, uid int64) ([]*types.SessionInfo, error) {
	rows, err := postgresSubmissionTx{dbs.Tx()}.QueryContext(dbs.Ctx(), `SELECT id, uid, scope, client, expires_at, ip_addr FROM session WHERE uid=?`, uid)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]*types.SessionInfo, 0)

	for rows.Next() {
		s := &types.SessionInfo{}
		err := rows.Scan(&s.ID, &s.UID, &s.Scope, &s.Client, &s.ExpiresAt, &s.IpAddr)
		if err != nil {
			return nil, err
		}
		result = append(result, s)
	}

	return result, rows.Err()
}

// GetSessionAuthInfo returns user ID + scope and/or expiration state
func (d *postgresSubmissionDAL) GetSessionAuthInfo(dbs DBSession, secret string) (*types.SessionInfo, bool, error) {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `SELECT id, uid, scope, client, expires_at, ip_addr FROM session WHERE submission_text_key(secret)=submission_text_key(?)`, secret)

	s := &types.SessionInfo{}
	err := row.Scan(&s.ID, &s.UID, &s.Scope, &s.Client, &s.ExpiresAt, &s.IpAddr)
	if err != nil {
		return nil, false, err
	}

	if !s.ExpiresAt.After(time.Now()) {
		return nil, false, nil
	}

	return s, true, nil
}

func (d *postgresSubmissionDAL) RevokeSession(dbs DBSession, uid int64, sessionID int64) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `DELETE FROM session WHERE uid=? AND id=?`, uid, sessionID)
	return err
}

// StoreDiscordUser store discord user or replace with new data
func (d *postgresSubmissionDAL) StoreDiscordUser(dbs DBSession, discordUser *types.DiscordUser) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(),
		`INSERT INTO discord_user (id, username, avatar, discriminator, public_flags, flags, locale, mfa_enabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			   ON CONFLICT (id) DO UPDATE SET username=EXCLUDED.username, avatar=EXCLUDED.avatar, discriminator=EXCLUDED.discriminator, public_flags=EXCLUDED.public_flags, flags=EXCLUDED.flags, locale=EXCLUDED.locale, mfa_enabled=EXCLUDED.mfa_enabled`,
		discordUser.ID, discordUser.Username, discordUser.Avatar, discordUser.Discriminator, discordUser.PublicFlags, discordUser.Flags, discordUser.Locale, discordUser.MFAEnabled)
	return err
}

// GetDiscordUser returns DiscordUserResponse
func (d *postgresSubmissionDAL) GetDiscordUser(dbs DBSession, uid int64) (*types.DiscordUser, error) {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `SELECT username, avatar, discriminator, public_flags, flags, locale, mfa_enabled FROM discord_user WHERE id=?`, uid)

	discordUser := &types.DiscordUser{ID: uid}
	err := row.Scan(&discordUser.Username, &discordUser.Avatar, &discordUser.Discriminator, &discordUser.PublicFlags, &discordUser.Flags, &discordUser.Locale, &discordUser.MFAEnabled)
	if err != nil {
		return nil, err
	}

	return discordUser, nil
}

// StoreDiscordServerRoles store discord user or replace with new data
func (d *postgresSubmissionDAL) StoreDiscordServerRoles(dbs DBSession, roles []types.DiscordRole) error {
	if len(roles) == 0 {
		return nil
	}
	data := make([]interface{}, 0, len(roles)*3)
	for _, role := range roles {
		data = append(data, role.ID, role.Name, role.Color)
	}

	const valuePlaceholder = `(?, ?, ?)`
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(),
		`INSERT INTO discord_role (id, name, color) VALUES `+valuePlaceholder+strings.Repeat(`,`+valuePlaceholder, len(roles)-1)+` ON CONFLICT DO NOTHING`,
		data...)
	return err
}

// StoreDiscordUserRoles store discord user roles
func (d *postgresSubmissionDAL) StoreDiscordUserRoles(dbs DBSession, uid int64, roles []int64) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `DELETE FROM discord_user_role WHERE fk_uid = ?`, uid)
	if err != nil {
		return err
	}

	if len(roles) == 0 {
		return nil
	}
	data := make([]interface{}, 0, len(roles)*3)
	for _, role := range roles {
		data = append(data, uid, role)
	}

	const valuePlaceholder = `(?, ?)`
	_, err = postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(),
		`INSERT INTO discord_user_role (fk_uid, fk_rid) VALUES `+valuePlaceholder+strings.Repeat(`,`+valuePlaceholder, len(roles)-1),
		data...)
	return err
}

// GetDiscordUserRoles returns all user roles
func (d *postgresSubmissionDAL) GetDiscordUserRoles(dbs DBSession, uid int64) ([]string, error) {
	rows, err := postgresSubmissionTx{dbs.Tx()}.QueryContext(dbs.Ctx(), `
		SELECT (SELECT name FROM discord_role WHERE discord_role.id=discord_user_role.fk_rid) FROM discord_user_role WHERE fk_uid=?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]string, 0)

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result = append(result, name)
	}

	return result, rows.Err()
}

func (d *postgresSubmissionDAL) SetClientSecret(dbs DBSession, clientID string, clientSecret string) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `INSERT INTO oauth_client (client_id, client_secret) VALUES (?, ?) ON CONFLICT (submission_general_text_key(client_id)) DO UPDATE SET client_secret=EXCLUDED.client_secret`, clientID, clientSecret)
	return err
}

func (d *postgresSubmissionDAL) GetClientSecret(dbs DBSession, clientID string) (string, error) {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `SELECT client_secret FROM oauth_client WHERE submission_general_text_key(client_id)=submission_general_text_key(?)`, clientID)

	var clientSecret string
	err := row.Scan(&clientSecret)
	if err != nil {
		return "", err
	}
	return clientSecret, nil
}

// StoreSubmission stores plain submission
func (d *postgresSubmissionDAL) StoreSubmission(dbs DBSession, submissionLevel string) (int64, error) {
	var sid int64
	err := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `INSERT INTO submission (fk_submission_level_id) 
				VALUES ((SELECT id FROM submission_level WHERE submission_text_key(name) = submission_text_key(?))) RETURNING id`,
		submissionLevel).Scan(&sid)
	if err != nil {
		return 0, err
	}

	_, err = postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		INSERT INTO submission_cache (fk_submission_id) 
		VALUES (?)`,
		sid)
	if err != nil {
		return 0, err
	}

	return sid, nil
}

// StoreSubmissionFile stores submission file
func (d *postgresSubmissionDAL) StoreSubmissionFile(dbs DBSession, s *types.SubmissionFile) (int64, error) {
	var fid int64
	err := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `INSERT INTO submission_file (fk_user_id, fk_submission_id, original_filename, current_filename, size, created_at, md5sum, sha256sum) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		s.SubmitterID, s.SubmissionID, s.OriginalFilename, s.CurrentFilename, s.Size, s.UploadedAt, s.MD5Sum, s.SHA256Sum).Scan(&fid)
	if err != nil {
		return 0, err
	}

	return fid, nil
}

// GetSubmissionFiles gets submission files, returns error if input len != output len
func (d *postgresSubmissionDAL) GetSubmissionFiles(dbs DBSession, sfids []int64) ([]*types.SubmissionFile, error) {
	if len(sfids) == 0 {
		return nil, nil
	}

	data := make([]interface{}, len(sfids))
	for i, d := range sfids {
		data[i] = d
	}

	q := `
		SELECT id, fk_user_id, fk_submission_id, original_filename, current_filename, size, created_at, md5sum, sha256sum 
		FROM submission_file 
		WHERE id IN(?` + strings.Repeat(",?", len(sfids)-1) + `)
		AND deleted_at IS NULL
		ORDER BY created_at DESC`

	var rows *sql.Rows
	var err error
	rows, err = postgresSubmissionTx{dbs.Tx()}.QueryContext(dbs.Ctx(), q, data...)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var result = make([]*types.SubmissionFile, 0, len(sfids))
	for rows.Next() {
		sf := &types.SubmissionFile{}
		err := rows.Scan(&sf.ID, &sf.SubmitterID, &sf.SubmissionID, &sf.OriginalFilename, &sf.CurrentFilename, &sf.Size, &sf.UploadedAt, &sf.MD5Sum, &sf.SHA256Sum)
		if err != nil {
			return nil, err
		}
		result = append(result, sf)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) != len(sfids) {
		return nil, fmt.Errorf("%d files were not found", len(result)-len(sfids))
	}

	return result, rows.Err()
}

// GetExtendedSubmissionFilesBySubmissionID returns all extended submission files for a given submission
func (d *postgresSubmissionDAL) GetExtendedSubmissionFilesBySubmissionID(dbs DBSession, sid int64) ([]*types.ExtendedSubmissionFile, error) {
	rows, err := postgresSubmissionTx{dbs.Tx()}.QueryContext(dbs.Ctx(), `
		SELECT submission_file.id, fk_user_id, username, avatar, 
		       original_filename, current_filename, size, created_at, md5sum, sha256sum 
		FROM submission_file 
		LEFT JOIN discord_user ON fk_user_id=discord_user.id
		WHERE fk_submission_id=?
		AND submission_file.deleted_at IS NULL
		ORDER BY created_at DESC`, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result = make([]*types.ExtendedSubmissionFile, 0)
	var avatar string
	for rows.Next() {
		sf := &types.ExtendedSubmissionFile{SubmissionID: sid}
		err := rows.Scan(&sf.FileID, &sf.SubmitterID, &sf.SubmitterUsername, &avatar,
			&sf.OriginalFilename, &sf.CurrentFilename, &sf.Size, &sf.UploadedAt, &sf.MD5Sum, &sf.SHA256Sum)
		if err != nil {
			return nil, err
		}
		sf.SubmitterAvatarURL = utils.FormatAvatarURL(sf.SubmitterID, avatar)
		result = append(result, sf)
	}
	return result, rows.Err()
}

// StoreCurationMeta stores curation meta
func (d *postgresSubmissionDAL) StoreCurationMeta(dbs DBSession, cm *types.CurationMeta) error {
	if cm.RuffleSupport == nil {
		empty := ""
		cm.RuffleSupport = &empty
	}

	var additionalApps []byte
	var err error
	if cm.AdditionalApps != nil {
		additionalApps, err = json.Marshal(cm.AdditionalApps)
		if err != nil {
			return err
		}
	}

	_, err = postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `INSERT INTO curation_meta (fk_submission_file_id, application_path, developer, extreme, game_notes, languages,
                           launch_command, original_description, play_mode, platform, publisher, release_date, series, source, status,
                           tags, tag_categories, title, alternate_titles, library, version, curation_notes, mount_parameters, uuid, game_exists,
                           primary_platform, ruffle_support, additional_applications)
                           VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cm.SubmissionFileID, cm.ApplicationPath, cm.Developer, cm.Extreme, cm.GameNotes, cm.Languages,
		cm.LaunchCommand, cm.OriginalDescription, cm.PlayMode, cm.Platform, cm.Publisher, cm.ReleaseDate, cm.Series, cm.Source, cm.Status,
		cm.Tags, cm.TagCategories, cm.Title, cm.AlternateTitles, cm.Library, cm.Version, cm.CurationNotes, cm.MountParameters, cm.UUID, cm.GameExists,
		cm.PrimaryPlatform, cm.RuffleSupport, postgresSubmissionJSON(additionalApps))
	return err
}

// GetCurationMetaBySubmissionFileID returns curation meta for given submission file
func (d *postgresSubmissionDAL) GetCurationMetaBySubmissionFileID(dbs DBSession, sfid int64) (*types.CurationMeta, error) {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `SELECT submission_file.fk_submission_id, application_path, developer, extreme, game_notes, languages,
                           launch_command, original_description, play_mode, platform, publisher, release_date, series, source, status,
                           tags, tag_categories, title, alternate_titles, library, version, curation_notes, mount_parameters, uuid, game_exists,
                           primary_platform, ruffle_support, additional_applications
		FROM curation_meta JOIN submission_file ON curation_meta.fk_submission_file_id = submission_file.id
		WHERE fk_submission_file_id=? AND submission_file.deleted_at IS NULL`, sfid)

	c := &types.CurationMeta{SubmissionFileID: sfid}
	var additionalApps []byte
	err := row.Scan(&c.SubmissionID, &c.ApplicationPath, &c.Developer, &c.Extreme, &c.GameNotes, &c.Languages,
		&c.LaunchCommand, &c.OriginalDescription, &c.PlayMode, &c.Platform, &c.Publisher, &c.ReleaseDate, &c.Series, &c.Source, &c.Status,
		&c.Tags, &c.TagCategories, &c.Title, &c.AlternateTitles, &c.Library, &c.Version, &c.CurationNotes, &c.MountParameters, &c.UUID, &c.GameExists,
		&c.PrimaryPlatform, &c.RuffleSupport, &additionalApps)
	if err != nil {
		return nil, err
	}

	if additionalApps != nil {
		err = json.Unmarshal(additionalApps, &c.AdditionalApps)
		if err != nil {
			return nil, err
		}
	}

	return c, nil
}

// StoreComment stores curation meta
func (d *postgresSubmissionDAL) StoreComment(dbs DBSession, c *types.Comment) (int64, error) {
	var msg *string
	if c.Message != nil {
		s := strings.TrimSpace(*c.Message)
		msg = &s
	}
	var id int64
	err := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `
		INSERT INTO comment (fk_user_id, fk_submission_id, message, fk_action_id, created_at) 
        VALUES (?, ?, ?, (SELECT id FROM action WHERE submission_text_key(name)=submission_text_key(?)), ?) RETURNING id`,
		c.AuthorID, c.SubmissionID, msg, c.Action, c.CreatedAt).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (d *postgresSubmissionDAL) PopulateRevisionInfo(dbs DBSession, revisions []*types.RevisionInfo) error {
	for _, revision := range revisions {
		var avatar string
		err := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `SELECT username, avatar
		FROM discord_user
		WHERE discord_user.id = ?`,
			revision.AuthorID).
			Scan(&revision.Username, &avatar)
		if err != nil {
			return err
		}
		revision.AvatarURL = utils.FormatAvatarURL(revision.AuthorID, avatar)
	}
	return nil
}

// GetExtendedCommentsBySubmissionID returns all comments with author data for a given submission
func (d *postgresSubmissionDAL) GetExtendedCommentsBySubmissionID(dbs DBSession, sid int64) ([]*types.ExtendedComment, error) {
	rows, err := postgresSubmissionTx{dbs.Tx()}.QueryContext(dbs.Ctx(), `
		SELECT comment.id, discord_user.id, username, avatar, message, (SELECT name FROM action WHERE id=comment.fk_action_id) as action, created_at 
		FROM comment 
		JOIN discord_user ON discord_user.id = fk_user_id
		WHERE fk_submission_id=? 
		AND comment.deleted_at IS NULL
		ORDER BY comment.created_at ASC, comment.id ASC;`, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]*types.ExtendedComment, 0)

	var avatar string

	for rows.Next() {

		ec := &types.ExtendedComment{SubmissionID: sid}
		if err := rows.Scan(&ec.CommentID, &ec.AuthorID, &ec.Username, &avatar, &ec.Message, &ec.Action, &ec.CreatedAt); err != nil {
			return nil, err
		}
		ec.AvatarURL = utils.FormatAvatarURL(ec.AuthorID, avatar)
		result = append(result, ec)
	}

	return result, rows.Err()
}

// GetCommentByID returns a comment
func (d *postgresSubmissionDAL) GetCommentByID(dbs DBSession, cid int64) (*types.Comment, error) {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `
		SELECT fk_user_id, fk_submission_id, message, (SELECT name FROM action WHERE id=comment.fk_action_id), created_at
		FROM comment
		WHERE id = ?`,
		cid)

	c := &types.Comment{}
	if err := row.Scan(&c.AuthorID, &c.SubmissionID, &c.Message, &c.Action, &c.CreatedAt); err != nil {
		return nil, err
	}

	return c, nil
}

// SoftDeleteSubmissionFile marks submission file as deleted
func (d *postgresSubmissionDAL) SoftDeleteSubmissionFile(dbs DBSession, sfid int64, deleteReason string) error {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `
		SELECT COUNT(*), fk_submission_id FROM submission_file
		WHERE fk_submission_id = (SELECT fk_submission_id FROM submission_file WHERE id = ?)
        AND submission_file.deleted_at IS NULL
		GROUP BY fk_submission_id`,
		sfid)

	var count int64
	var sid int64
	if err := row.Scan(&count, &sid); err != nil {
		return err
	}
	if count <= 1 {
		return fmt.Errorf(constants.ErrorCannotDeleteLastSubmissionFile)
	}

	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		UPDATE submission_file SET deleted_at = (statement_timestamp() AT TIME ZONE 'UTC'), deleted_reason = ?
		WHERE id  = ?`,
		deleteReason, sfid)
	if err != nil {
		return err
	}

	err = d.UpdateSubmissionCacheTable(dbs, sid)
	if err != nil {
		return err
	}

	return nil
}

// SoftDeleteSubmission marks submission and its files as deleted
func (d *postgresSubmissionDAL) SoftDeleteSubmission(dbs DBSession, sid int64, deleteReason string) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		UPDATE submission_file SET deleted_at = (statement_timestamp() AT TIME ZONE 'UTC'), deleted_reason = ?
		WHERE fk_submission_id = ?`,
		deleteReason, sid)
	if err != nil {
		return err
	}

	_, err = postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		UPDATE comment SET deleted_at = (statement_timestamp() AT TIME ZONE 'UTC'), deleted_reason = ?
		WHERE fk_submission_id = ?`,
		deleteReason, sid)
	if err != nil {
		return err
	}

	_, err = postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		UPDATE submission SET deleted_at = (statement_timestamp() AT TIME ZONE 'UTC'), deleted_reason = ?
		WHERE id = ?`,
		deleteReason, sid)
	if err != nil {
		return err
	}

	err = d.UpdateSubmissionCacheTable(dbs, sid)
	if err != nil {
		return err
	}

	return nil
}

// SoftDeleteComment marks comment as deleted
func (d *postgresSubmissionDAL) SoftDeleteComment(dbs DBSession, cid int64, deleteReason string) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		UPDATE comment SET deleted_at = (statement_timestamp() AT TIME ZONE 'UTC'), deleted_reason = ?
		WHERE id = ?`,
		deleteReason, cid)
	if err != nil {
		return err
	}

	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `
		SELECT fk_submission_id FROM comment
		WHERE id = ?`,
		cid)

	var sid int64
	err = row.Scan(&sid)
	if err != nil {
		return err
	}

	err = d.UpdateSubmissionCacheTable(dbs, sid)
	if err != nil {
		return err
	}

	return nil
}

// StoreNotificationSettings clears and stores new notification settings for user
func (d *postgresSubmissionDAL) StoreNotificationSettings(dbs DBSession, uid int64, actions []string) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		DELETE FROM notification_settings WHERE fk_user_id = ?`,
		uid)
	if err != nil {
		return err
	}

	if len(actions) == 0 {
		return nil
	}
	data := make([]interface{}, 0, len(actions)*2)
	for _, role := range actions {
		data = append(data, uid, role)
	}

	const valuePlaceholder = `(?, (SELECT id FROM action WHERE submission_text_key(name) = submission_text_key(?)))`
	_, err = postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(),
		`INSERT INTO notification_settings (fk_user_id, fk_action_id) VALUES `+valuePlaceholder+strings.Repeat(`,`+valuePlaceholder, len(actions)-1)+` ON CONFLICT (fk_user_id, fk_action_id) DO NOTHING`,
		data...)
	return err
}

// GetNotificationSettingsByUserID returns actions on which user is notified on submissions he's subscribed to
func (d *postgresSubmissionDAL) GetNotificationSettingsByUserID(dbs DBSession, uid int64) ([]string, error) {
	rows, err := postgresSubmissionTx{dbs.Tx()}.QueryContext(dbs.Ctx(), `
		SELECT (SELECT name FROM action WHERE action.id = notification_settings.fk_action_id) AS action_name
		FROM notification_settings 
		WHERE fk_user_id = ?`,
		uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]string, 0)
	var action string

	for rows.Next() {
		if err := rows.Scan(&action); err != nil {
			return nil, err
		}
		result = append(result, action)
	}

	return result, rows.Err()
}

// SubscribeUserToSubmission stores subscription to a submission
func (d *postgresSubmissionDAL) SubscribeUserToSubmission(dbs DBSession, uid, sid int64) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		INSERT INTO submission_notification_subscription (fk_user_id, fk_submission_id, created_at)
		VALUES (?, ?, (statement_timestamp() AT TIME ZONE 'UTC')) ON CONFLICT (fk_user_id, fk_submission_id) DO NOTHING`,
		uid, sid)
	return err
}

// UnsubscribeUserFromSubmission deletes subscription to a submission
func (d *postgresSubmissionDAL) UnsubscribeUserFromSubmission(dbs DBSession, uid, sid int64) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		DELETE FROM submission_notification_subscription
		WHERE fk_user_id = ? AND fk_submission_id = ?`,
		uid, sid)
	return err
}

// IsUserSubscribedToSubmission returns true if the user is subscribed to a submission
func (d *postgresSubmissionDAL) IsUserSubscribedToSubmission(dbs DBSession, uid, sid int64) (bool, error) {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `
		SELECT COUNT(*) FROM submission_notification_subscription
		WHERE fk_user_id = ? AND fk_submission_id = ?`,
		uid, sid)

	var count uint64

	err := row.Scan(&count)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// StoreNotification stores a notification message in the database which acts as a queue for the notification service
func (d *postgresSubmissionDAL) StoreNotification(dbs DBSession, msg, notificationType string) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		INSERT INTO submission_notification (message, fk_submission_notification_type_id, created_at)
		VALUES(?, (SELECT id FROM submission_notification_type WHERE submission_text_key(name) = submission_text_key(?)), (statement_timestamp() AT TIME ZONE 'UTC'))`,
		msg, notificationType)

	return err
}

// GetUsersForNotification returns a list of users who should be notified by an event
func (d *postgresSubmissionDAL) GetUsersForNotification(dbs DBSession, authorID, sid int64, action string) ([]int64, error) {
	rows, err := postgresSubmissionTx{dbs.Tx()}.QueryContext(dbs.Ctx(), `
		SELECT DISTINCT notification_settings.fk_user_id
		FROM notification_settings
		LEFT JOIN submission_notification_subscription ON submission_notification_subscription.fk_user_id = notification_settings.fk_user_id
		WHERE submission_notification_subscription.fk_submission_id = ?
		AND notification_settings.fk_action_id = (SELECT id FROM action where submission_text_key(name) = submission_text_key(?))
		AND notification_settings.fk_user_id != ?`,
		sid, action, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]int64, 0)
	var uid int64

	for rows.Next() {
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		result = append(result, uid)
	}

	return result, rows.Err()
}

// GetUsersForUniversalNotification returns a list of users who should be notified by an event not dependent on a submission ID
func (d *postgresSubmissionDAL) GetUsersForUniversalNotification(dbs DBSession, authorID int64, action string) ([]int64, error) {
	rows, err := postgresSubmissionTx{dbs.Tx()}.QueryContext(dbs.Ctx(), `
		SELECT DISTINCT fk_user_id
		FROM notification_settings
		WHERE fk_action_id = (SELECT id FROM action where submission_text_key(name) = submission_text_key(?))
		AND fk_user_id != ?`,
		action, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]int64, 0)
	var uid int64

	for rows.Next() {
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		result = append(result, uid)
	}

	return result, rows.Err()
}

// GetOldestUnsentNotification returns oldest unsent notification
func (d *postgresSubmissionDAL) GetOldestUnsentNotification(dbs DBSession) (*types.Notification, error) {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `
		SELECT id, (SELECT name FROM submission_notification_type WHERE id = fk_submission_notification_type_id), message, created_at, sent_at 
		FROM submission_notification
		WHERE sent_at IS NULL
		ORDER BY created_at LIMIT 1`)

	notification := &types.Notification{}
	var sentAt *time.Time

	err := row.Scan(&notification.ID, &notification.Type, &notification.Message, &notification.CreatedAt, &sentAt)
	if err != nil {
		return nil, err
	}

	if sentAt != nil {
		notification.SentAt = *sentAt
	}

	return notification, nil
}

// MarkNotificationAsSent returns oldest unsent notification
func (d *postgresSubmissionDAL) MarkNotificationAsSent(dbs DBSession, nid int64) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		UPDATE submission_notification SET sent_at = (statement_timestamp() AT TIME ZONE 'UTC') 
		WHERE id = ?`, nid)

	return err
}

// StoreCurationImage stores curation image
func (d *postgresSubmissionDAL) StoreCurationImage(dbs DBSession, c *types.CurationImage) (int64, error) {
	var id int64
	err := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `
		INSERT INTO curation_image (fk_submission_file_id, fk_curation_image_type_id, filename) 
		VALUES (?, (SELECT id FROM curation_image_type WHERE submission_text_key(name) = submission_text_key(?)), ?) RETURNING id`,
		c.SubmissionFileID, c.Type, c.Filename).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetCurationImagesBySubmissionFileID return images for a given submission file ID
func (d *postgresSubmissionDAL) GetCurationImagesBySubmissionFileID(dbs DBSession, sfid int64) ([]*types.CurationImage, error) {
	rows, err := postgresSubmissionTx{dbs.Tx()}.QueryContext(dbs.Ctx(), `
		SELECT id, (SELECT name FROM curation_image_type WHERE id = fk_curation_image_type_id), filename
		FROM curation_image
		WHERE fk_submission_file_id = ?`,
		sfid)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var result = make([]*types.CurationImage, 0)
	for rows.Next() {
		c := &types.CurationImage{SubmissionFileID: sfid}
		err := rows.Scan(&c.ID, &c.Type, &c.Filename)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}

	return result, rows.Err()
}

// GetCurationImage returns curation image
func (d *postgresSubmissionDAL) GetCurationImage(dbs DBSession, ciid int64) (*types.CurationImage, error) {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `
		SELECT fk_submission_file_id, (SELECT name FROM curation_image_type WHERE id = fk_curation_image_type_id), filename
		FROM curation_image
		WHERE id = ?`,
		ciid)

	ci := &types.CurationImage{ID: ciid}

	err := row.Scan(&ci.SubmissionFileID, &ci.Type, &ci.Filename)
	if err != nil {
		return nil, err
	}

	return ci, nil
}

// GetNextSubmission returns ID of next submission that's not deleted
func (d *postgresSubmissionDAL) GetNextSubmission(dbs DBSession, sid int64) (int64, error) {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `
		SELECT id
		FROM submission
		WHERE id > ? AND deleted_at IS NULL
		ORDER BY id
		LIMIT 1`,
		sid)

	var nsid int64

	err := row.Scan(&nsid)
	if err != nil {
		return 0, err
	}

	return nsid, nil
}

// GetPreviousSubmission returns ID of previous submission that's not deleted
func (d *postgresSubmissionDAL) GetPreviousSubmission(dbs DBSession, sid int64) (int64, error) {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `
		SELECT id
		FROM submission
		WHERE id < ? AND deleted_at IS NULL
		ORDER BY id DESC
		LIMIT 1`,
		sid)

	var psid int64

	err := row.Scan(&psid)
	if err != nil {
		return 0, err
	}

	return psid, nil
}

// ClearMasterDBGames clears the masterdb metadata table
func (d *postgresSubmissionDAL) ClearMasterDBGames(dbs DBSession) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `DELETE FROM masterdb_game`)
	return err
}

// StoreMasterDBGames stores games into the masterdb metadata table
func (d *postgresSubmissionDAL) StoreMasterDBGames(dbs DBSession, games []*types.MasterDatabaseGame) error {
	if len(games) == 0 {
		return nil
	}
	data := make([]interface{}, 0, len(games)*21)
	for _, g := range games {
		data = append(data, g.UUID, g.Title, g.AlternateTitles, g.Series, g.Developer, g.Publisher, g.Platform,
			g.Extreme, g.PlayMode, g.Status, g.GameNotes, g.Source, g.LaunchCommand, g.ReleaseDate,
			g.Version, g.OriginalDescription, g.Languages, g.Library, g.Tags, g.DateAdded, g.DateModified)
	}

	const valuePlaceholder = `(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(),
		`INSERT INTO masterdb_game (uuid, title, alternate_titles, series, developer, publisher, platform, extreme, play_mode, status, game_notes, source, launch_command, release_date, version, original_description, languages, library, tags, date_added, date_modified) VALUES 
		`+valuePlaceholder+strings.Repeat(`,`+valuePlaceholder, len(games)-1)+` ON CONFLICT DO NOTHING`,
		data...)
	return err
}

// GetAllSimilarityAttributes returns IDs, titles and, launch commands
func (d *postgresSubmissionDAL) GetAllSimilarityAttributes(dbs DBSession) ([]*types.SimilarityAttributes, error) {
	// MariaDB UNION deduplicates according to the source text collation. Keep
	// the original values for display/similarity computation, using keys only
	// for equality. A live row wins if a legacy row has the same three values.
	// Most identities occur only once, so their title/command cannot affect
	// deduplication or ordering. Compute those larger keys only for collisions;
	// all colliding rows still use the complete tuple and NULL semantics.
	rows, err := postgresSubmissionTx{dbs.Tx()}.QueryContext(dbs.Ctx(), `
		WITH candidates AS (
			SELECT submission.id::text AS identity, meta.title, meta.launch_command, 0 AS source_order
			FROM submission
			LEFT JOIN submission_cache ON submission_cache.fk_submission_id = submission.id
			LEFT JOIN submission_file AS newest_file ON newest_file.id = submission_cache.fk_newest_file_id
			LEFT JOIN curation_meta meta ON meta.fk_submission_file_id = newest_file.id
			WHERE submission.deleted_at IS NULL
			UNION ALL
			SELECT uuid, title, launch_command, 1 FROM masterdb_game
		), identities AS MATERIALIZED (
			SELECT *, submission_text_key(identity) AS identity_key
			FROM candidates
		), identity_groups AS (
			SELECT *, count(*) OVER (PARTITION BY identity_key) AS identity_count
			FROM identities
		), tuple_keys AS (
			SELECT *,
				CASE WHEN identity_count > 1 THEN submission_text_key(title) END AS title_key,
				CASE WHEN identity_count > 1 THEN submission_text_key(launch_command) END AS command_key
			FROM identity_groups
		)
		SELECT DISTINCT ON (identity_key, title_key, command_key)
			identity, title, launch_command
		FROM tuple_keys
		ORDER BY identity_key, title_key, command_key, source_order`)

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var result = make([]*types.SimilarityAttributes, 0, 100000)
	for rows.Next() {
		lc := &types.SimilarityAttributes{}
		err := rows.Scan(&lc.ID, &lc.Title, &lc.LaunchCommand)
		if err != nil {
			return nil, err
		}
		result = append(result, lc)
	}

	return result, rows.Err()
}

// DeleteUserSessions deletes all sessions of a given user, including inactive sessions
func (d *postgresSubmissionDAL) DeleteUserSessions(dbs DBSession, uid int64) (int64, error) {
	r, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		DELETE FROM session WHERE uid=?`,
		uid)
	if err != nil {
		return 0, err
	}

	count, err := r.RowsAffected()
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetTotalCommentsCount returns a total number of comments in the system
func (d *postgresSubmissionDAL) GetTotalCommentsCount(dbs DBSession) (int64, error) {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `
		SELECT COUNT(*) FROM comment`)

	var count int64
	if err := row.Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
}

// GetTotalUserCount returns a total number of users in the system
func (d *postgresSubmissionDAL) GetTotalUserCount(dbs DBSession) (int64, error) {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `
		SELECT COUNT(*) FROM discord_user`)

	var count int64
	if err := row.Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
}

// GetTotalSubmissionFilesize returns a total size of all uploaded submissions
func (d *postgresSubmissionDAL) GetTotalSubmissionFilesize(dbs DBSession) (int64, error) {
	row := postgresSubmissionTx{dbs.Tx()}.QueryRowContext(dbs.Ctx(), `
		SELECT SUM(size) FROM submission_file`)

	var count int64
	if err := row.Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
}

// GetUsers returns all users
func (d *postgresSubmissionDAL) GetUsers(dbs DBSession) ([]*types.User, error) {
	rows, err := postgresSubmissionTx{dbs.Tx()}.QueryContext(dbs.Ctx(), `SELECT id, username FROM discord_user`)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var result = make([]*types.User, 0)
	for rows.Next() {
		u := &types.User{}
		var uid int64
		err := rows.Scan(&uid, &u.Username)
		if err != nil {
			return nil, err
		}

		u.ID = fmt.Sprintf("%d", uid)

		result = append(result, u)
	}

	return result, rows.Err()
}

// GetCommentsByUserIDAndAction returns comments with an oddly specific filter
func (d *postgresSubmissionDAL) GetCommentsByUserIDAndAction(dbs DBSession, uid int64, action string) ([]*types.Comment, error) {
	rows, err := postgresSubmissionTx{dbs.Tx()}.QueryContext(dbs.Ctx(), `
		SELECT id, message, created_at
		FROM comment
		WHERE fk_user_id = ?
		AND deleted_at IS NULL
		AND fk_action_id = (SELECT id FROM action WHERE submission_text_key(name) = submission_text_key(?))
		ORDER BY created_at DESC`, uid, action)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]*types.Comment, 0)

	for rows.Next() {

		c := &types.Comment{AuthorID: uid, Action: action}
		if err := rows.Scan(&c.ID, &c.Message, &c.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, c)
	}

	return result, rows.Err()
}

// FreezeSubmission marks submission as frozen
func (d *postgresSubmissionDAL) FreezeSubmission(dbs DBSession, sid int64) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		UPDATE submission SET frozen_at = (statement_timestamp() AT TIME ZONE 'UTC')
		WHERE id = ?`,
		sid)
	if err != nil {
		return err
	}

	return nil
}

// UnfreezeSubmission removes freeze from a submission
func (d *postgresSubmissionDAL) UnfreezeSubmission(dbs DBSession, sid int64) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		UPDATE submission SET frozen_at = NULL
		WHERE id = ?`,
		sid)
	if err != nil {
		return err
	}

	return nil
}

// NukeSessionTable empties the session table
func (d *postgresSubmissionDAL) NukeSessionTable(dbs DBSession) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `DELETE from session`)
	return err
}

// UpdateSubmissionAutofreeze sets autofreeze to a given value
func (d *postgresSubmissionDAL) UpdateSubmissionAutofreeze(dbs DBSession, sid int64, shouldAutofreeze bool) error {
	_, err := postgresSubmissionTx{dbs.Tx()}.ExecContext(dbs.Ctx(), `
		UPDATE submission SET should_autofreeze = ?
		WHERE id = ?`,
		shouldAutofreeze, sid)
	if err != nil {
		return err
	}

	return nil
}

// PostgreSQL distinguishes this transaction from the retained MariaDB baseline.
func (dbs *PostgresSubmissionSession) PostgreSQL() bool { return true }

// postgresSubmissionTx binds the ordinary DAL's static question-mark SQL to
// PostgreSQL positional parameters. Only SQL authored in this file is accepted;
// user values always travel as separate parameters, never as query text.
type postgresSubmissionTx struct{ *sql.Tx }

func postgresSubmissionBind(query string) string {
	var result strings.Builder
	index := 0
	for _, ch := range query {
		if ch == '?' {
			index++
			fmt.Fprintf(&result, "$%d", index)
		} else {
			result.WriteRune(ch)
		}
	}
	return result.String()
}
func (tx postgresSubmissionTx) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return tx.Tx.ExecContext(ctx, postgresSubmissionBind(query), postgresSubmissionArgs(args)...)
}
func (tx postgresSubmissionTx) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return tx.Tx.QueryContext(ctx, postgresSubmissionBind(query), postgresSubmissionArgs(args)...)
}
func (tx postgresSubmissionTx) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return tx.Tx.QueryRowContext(ctx, postgresSubmissionBind(query), postgresSubmissionArgs(args)...)
}
func postgresSubmissionJSON(value []byte) interface{} {
	if value == nil {
		return nil
	}
	return string(value)
}

// MariaDB connections encode time arguments in UTC. pgx's timestamp-without-
// time-zone codec instead retains the argument's wall clock, so normalize before
// passing values to it. Copy both the argument slice and pointed-to times: DAL
// callers may reuse their values after this call.
func postgresSubmissionArgs(args []interface{}) []interface{} {
	result := append([]interface{}(nil), args...)
	for i, arg := range result {
		switch value := arg.(type) {
		case time.Time:
			result[i] = value.UTC()
		case *time.Time:
			if value != nil {
				normalized := value.UTC()
				result[i] = &normalized
			}
		}
	}
	return result
}
