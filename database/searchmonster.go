package database

import (
	"strconv"
	"strings"
	"time"

	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
)

// SearchSubmissions returns extended submissions based on given filter
func (d *mysqlDAL) SearchSubmissions(dbs DBSession, filter *types.SubmissionsFilter) ([]*types.ExtendedSubmission, int64, error) {
	uid := utils.UserID(dbs.Ctx()) // TODO this should be passed as param

	filters := make([]string, 0)
	masterFilters := make([]string, 0)
	data := make([]interface{}, 0)
	masterData := make([]interface{}, 0)

	const defaultLimit int64 = 100
	const defaultOffset int64 = 0
	const defaultOrderBy string = "updated_at"
	const defaultSortOrder string = "DESC"

	currentLimit := defaultLimit
	currentOffset := defaultOffset
	currentOrderBy := defaultOrderBy
	currentSortOrder := defaultSortOrder

	if filter != nil {
		// Validate a copy: direct DAL callers need the same guarantees as HTTP
		// callers, without normalizing the caller's filter in place.
		normalized := *filter
		if err := normalized.Validate(); err != nil {
			return nil, 0, err
		}
		filter = &normalized
		if len(filter.SubmissionIDs) > 0 {
			filters = append(filters, `(submission.id IN(?`+strings.Repeat(`,?`, len(filter.SubmissionIDs)-1)+`))`)
			for _, sid := range filter.SubmissionIDs {
				data = append(data, sid)
			}
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.SubmitterID != nil {
			filters = append(filters, "(uploader.id = ?)")
			data = append(data, *filter.SubmitterID)
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.TitlePartial != nil {
			filters = append(filters, "(meta.title LIKE ? OR meta.alternate_titles LIKE ?)")
			data = append(data, utils.FormatLike(*filter.TitlePartial), utils.FormatLike(*filter.TitlePartial))
			masterFilters = append(masterFilters, "(title LIKE ? OR alternate_titles LIKE ?)")
			masterData = append(masterData, utils.FormatLike(*filter.TitlePartial), utils.FormatLike(*filter.TitlePartial))
		}
		if filter.CommentPartial != nil {
			masterFilters = append(masterFilters, "(1 = 0)") // legacy entries have no comments
		}
		if filter.SubmitterUsernamePartial != nil {
			tableName := `uploader.username`
			filters, masterFilters, data, masterData = addMultifilter(
				tableName, nil, *filter.SubmitterUsernamePartial, filters, masterFilters, data, masterData)
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.PlatformPartial != nil {
			tableName := `meta.platform`
			masterTableName := `platform`
			filters, masterFilters, data, masterData = addMultifilter(
				tableName, &masterTableName, *filter.PlatformPartial, filters, masterFilters, data, masterData)
		}
		if filter.LibraryPartial != nil {
			filters = append(filters, "(meta.library LIKE ?)")
			data = append(data, utils.FormatLike(*filter.LibraryPartial))
			masterFilters = append(masterFilters, "(library LIKE ?)")
			masterData = append(masterData, utils.FormatLike(*filter.LibraryPartial))
		}
		// Metadata filters use the newest active file, just like Title/Platform.
		// Column names are constants; all user-supplied values remain parameters.
		for _, textFilter := range []struct {
			column string
			value  *string
		}{
			{"series", filter.SeriesPartial},
			{"developer", filter.DeveloperPartial},
			{"publisher", filter.PublisherPartial},
			{"play_mode", filter.PlayModePartial},
			{"status", filter.StatusPartial},
			{"version", filter.VersionPartial},
			{"release_date", filter.ReleaseDatePartial},
			{"languages", filter.LanguagePartial},
			{"source", filter.SourcePartial},
			{"game_notes", filter.GameNotesPartial},
			{"curation_notes", filter.CurationNotesPartial},
			{"original_description", filter.OriginalDescriptionPartial},
		} {
			if textFilter.value == nil {
				continue
			}
			filters = append(filters, "(meta."+textFilter.column+" LIKE ?)")
			data = append(data, utils.FormatLike(*textFilter.value))
			if textFilter.column == "curation_notes" {
				masterFilters = append(masterFilters, "(1 = 0)") // not stored in legacy metadata
			} else {
				masterFilters = append(masterFilters, "("+textFilter.column+" LIKE ?)")
				masterData = append(masterData, utils.FormatLike(*textFilter.value))
			}
		}
		if filter.TagsPartial != nil {
			masterColumn := "tags"
			filters, masterFilters, data, masterData = addMultifilter(
				"meta.tags", &masterColumn, *filter.TagsPartial, filters, masterFilters, data, masterData)
		}
		if filter.HasAdditionalApplications != nil {
			comparison := " > 0"
			if *filter.HasAdditionalApplications == "no" {
				comparison = " = 0"
			}
			// Missing/empty lists (SQL NULL, JSON null, []) have no apps. A
			// missing metadata row is unknown, as are legacy entries without this field.
			filters = append(filters, "(meta.id IS NOT NULL AND (CASE WHEN JSON_TYPE(meta.additional_applications) = 'ARRAY' THEN JSON_LENGTH(meta.additional_applications) ELSE 0 END)"+comparison+")")
			masterFilters = append(masterFilters, "(1 = 0)")
		}
		if filter.OriginalFilenamePartialAny != nil {
			filters = append(filters, "(submission_cache.original_filename_sequence LIKE ?)")
			data = append(data, utils.FormatLike(*filter.OriginalFilenamePartialAny))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.CurrentFilenamePartialAny != nil {
			filters = append(filters, "(submission_cache.current_filename_sequence LIKE ?)")
			data = append(data, utils.FormatLike(*filter.CurrentFilenamePartialAny))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.MD5SumPartialAny != nil {
			filters = append(filters, "(submission_cache.md5sum_sequence LIKE ?)")
			data = append(data, utils.FormatLike(*filter.MD5SumPartialAny))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.SHA256SumPartialAny != nil {
			filters = append(filters, "(submission_cache.sha256sum_sequence LIKE ?)")
			data = append(data, utils.FormatLike(*filter.SHA256SumPartialAny))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if len(filter.BotActions) != 0 {
			filters = append(filters, `(submission_cache.bot_action IN(?`+strings.Repeat(",?", len(filter.BotActions)-1)+`))`)
			for _, ba := range filter.BotActions {
				data = append(data, ba)
			}
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if len(filter.SubmissionLevels) != 0 {
			filters = append(filters, `((SELECT name FROM submission_level WHERE id = submission.fk_submission_level_id) IN(?`+strings.Repeat(",?", len(filter.SubmissionLevels)-1)+`))`)
			for _, ba := range filter.SubmissionLevels {
				data = append(data, ba)
			}
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}

		if filter.ResultsPerPage != nil {
			currentLimit = *filter.ResultsPerPage
		} else {
			currentLimit = defaultLimit
		}
		if filter.Page != nil {
			currentOffset = (*filter.Page - 1) * currentLimit
		} else {
			currentOffset = defaultOffset
		}
		if filter.AssignedStatusTesting != nil {
			if *filter.AssignedStatusTesting == "unassigned" {
				filters = append(filters, "(submission_cache.active_assigned_testing_ids IS NULL)")
			} else if *filter.AssignedStatusTesting == "assigned" {
				filters = append(filters, "(submission_cache.active_assigned_testing_ids IS NOT NULL)")
			}
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.AssignedStatusVerification != nil {
			if *filter.AssignedStatusVerification == "unassigned" {
				filters = append(filters, "(submission_cache.active_assigned_verification_ids IS NULL)")
			} else if *filter.AssignedStatusVerification == "assigned" {
				filters = append(filters, "(submission_cache.active_assigned_verification_ids IS NOT NULL)")
			}
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.RequestedChangedStatus != nil {
			if *filter.RequestedChangedStatus == "none" {
				filters = append(filters, "(submission_cache.active_requested_changes_ids IS NULL)")
			} else if *filter.RequestedChangedStatus == "ongoing" {
				filters = append(filters, "(submission_cache.active_requested_changes_ids IS NOT NULL)")
			}
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.ApprovalsStatus != nil {
			if *filter.ApprovalsStatus == "none" {
				filters = append(filters, "(submission_cache.active_approved_ids IS NULL)")
			} else if *filter.ApprovalsStatus == "approved" {
				filters = append(filters, "(submission_cache.active_approved_ids IS NOT NULL)")
			}
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.VerificationStatus != nil {
			if *filter.VerificationStatus == "none" {
				filters = append(filters, "(submission_cache.active_verified_ids IS NULL)")
			} else if *filter.VerificationStatus == "verified" {
				filters = append(filters, "(submission_cache.active_verified_ids IS NOT NULL)")
			}
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.AssignedStatusTestingMe != nil {
			if *filter.AssignedStatusTestingMe == "unassigned" {
				filters = append(filters, "(COALESCE(FIND_IN_SET(?, submission_cache.active_assigned_testing_ids), 0) = 0)")
			} else if *filter.AssignedStatusTestingMe == "assigned" {
				filters = append(filters, "(FIND_IN_SET(?, submission_cache.active_assigned_testing_ids) > 0)")
			}
			data = append(data, strconv.FormatInt(uid, 10))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.AssignedStatusVerificationMe != nil {
			if *filter.AssignedStatusVerificationMe == "unassigned" {
				filters = append(filters, "(COALESCE(FIND_IN_SET(?, submission_cache.active_assigned_verification_ids), 0) = 0)")
			} else if *filter.AssignedStatusVerificationMe == "assigned" {
				filters = append(filters, "(FIND_IN_SET(?, submission_cache.active_assigned_verification_ids) > 0)")
			}
			data = append(data, strconv.FormatInt(uid, 10))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.RequestedChangedStatusMe != nil {
			if *filter.RequestedChangedStatusMe == "none" {
				filters = append(filters, "(COALESCE(FIND_IN_SET(?, submission_cache.active_requested_changes_ids), 0) = 0)")
			} else if *filter.RequestedChangedStatusMe == "ongoing" {
				filters = append(filters, "(FIND_IN_SET(?, submission_cache.active_requested_changes_ids) > 0)")
			}
			data = append(data, strconv.FormatInt(uid, 10))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.ApprovalsStatusMe != nil {
			if *filter.ApprovalsStatusMe == "no" {
				filters = append(filters, "(COALESCE(FIND_IN_SET(?, submission_cache.active_approved_ids), 0) = 0)")
			} else if *filter.ApprovalsStatusMe == "yes" {
				filters = append(filters, "(FIND_IN_SET(?, submission_cache.active_approved_ids) > 0)")
			}
			data = append(data, strconv.FormatInt(uid, 10))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.VerificationStatusMe != nil {
			if *filter.VerificationStatusMe == "no" {
				filters = append(filters, "(COALESCE(FIND_IN_SET(?, submission_cache.active_verified_ids), 0) = 0)")
			} else if *filter.VerificationStatusMe == "yes" {
				filters = append(filters, "(FIND_IN_SET(?, submission_cache.active_verified_ids) > 0)")
			}
			data = append(data, strconv.FormatInt(uid, 10))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}

		if filter.AssignedStatusTestingUser != nil {
			if *filter.AssignedStatusTestingUser == "unassigned" {
				filters = append(filters, "(COALESCE(FIND_IN_SET(?, submission_cache.active_assigned_testing_ids), 0) = 0)")
			} else if *filter.AssignedStatusTestingUser == "assigned" {
				filters = append(filters, "(FIND_IN_SET(?, submission_cache.active_assigned_testing_ids) > 0)")
			}
			data = append(data, strconv.FormatInt(*filter.AssignedStatusUserID, 10))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.AssignedStatusVerificationUser != nil {
			if *filter.AssignedStatusVerificationUser == "unassigned" {
				filters = append(filters, "(COALESCE(FIND_IN_SET(?, submission_cache.active_assigned_verification_ids), 0) = 0)")
			} else if *filter.AssignedStatusVerificationUser == "assigned" {
				filters = append(filters, "(FIND_IN_SET(?, submission_cache.active_assigned_verification_ids) > 0)")
			}
			data = append(data, strconv.FormatInt(*filter.AssignedStatusUserID, 10))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.RequestedChangedStatusUser != nil {
			if *filter.RequestedChangedStatusUser == "none" {
				filters = append(filters, "(COALESCE(FIND_IN_SET(?, submission_cache.active_requested_changes_ids), 0) = 0)")
			} else if *filter.RequestedChangedStatusUser == "ongoing" {
				filters = append(filters, "(FIND_IN_SET(?, submission_cache.active_requested_changes_ids) > 0)")
			}
			data = append(data, strconv.FormatInt(*filter.AssignedStatusUserID, 10))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.ApprovalsStatusUser != nil {
			if *filter.ApprovalsStatusUser == "no" {
				filters = append(filters, "(COALESCE(FIND_IN_SET(?, submission_cache.active_approved_ids), 0) = 0)")
			} else if *filter.ApprovalsStatusUser == "yes" {
				filters = append(filters, "(FIND_IN_SET(?, submission_cache.active_approved_ids) > 0)")
			}
			data = append(data, strconv.FormatInt(*filter.AssignedStatusUserID, 10))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.VerificationStatusUser != nil {
			if *filter.VerificationStatusUser == "no" {
				filters = append(filters, "(COALESCE(FIND_IN_SET(?, submission_cache.active_verified_ids), 0) = 0)")
			} else if *filter.VerificationStatusUser == "yes" {
				filters = append(filters, "(FIND_IN_SET(?, submission_cache.active_verified_ids) > 0)")
			}
			data = append(data, strconv.FormatInt(*filter.AssignedStatusUserID, 10))
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}

		if filter.IsExtreme != nil {
			filters = append(filters, "(meta.extreme = ?)")
			data = append(data, *filter.IsExtreme)
			masterFilters = append(masterFilters, "(extreme = ?)")
			masterData = append(masterData, *filter.IsExtreme)
		}
		if len(filter.DistinctActions) != 0 {
			filters = append(filters, `(submission_cache.distinct_actions REGEXP CONCAT(CONCAT(?)`+strings.Repeat(", '|', CONCAT(?)", len(filter.DistinctActions)-1)+`))`)
			for _, da := range filter.DistinctActions {
				data = append(data, da)
			}
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if len(filter.DistinctActionsNot) != 0 {
			filters = append(filters, `(submission_cache.distinct_actions NOT REGEXP CONCAT(CONCAT(?)`+strings.Repeat(", '|', CONCAT(?)", len(filter.DistinctActionsNot)-1)+`))`)
			for _, da := range filter.DistinctActionsNot {
				data = append(data, da)
			}
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.LaunchCommandFuzzy != nil { // TODO not really fuzzy is it
			filters = append(filters, "(meta.launch_command LIKE ?)")
			data = append(data, utils.FormatLike(*filter.LaunchCommandFuzzy))
			masterFilters = append(masterFilters, "(launch_command LIKE ?)")
			masterData = append(masterData, utils.FormatLike(*filter.LaunchCommandFuzzy))
		}
		if filter.LastUploaderNotMe != nil {
			if *filter.LastUploaderNotMe == "yes" {
				filters = append(filters, "(newest_file.fk_user_id != ?)")
				data = append(data, uid)
			}
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.SubmitterNotMe != nil {
			filters = append(filters, "(oldest_file.fk_user_id != ?)")
			data = append(data, uid)
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.OrderBy != nil {
			switch *filter.OrderBy {
			case "uploaded":
				currentOrderBy = "created_at"
			case "updated":
				currentOrderBy = "updated_at"
			case "size":
				currentOrderBy = "newest_file_size"
			case "title":
				currentOrderBy = "meta_title"
			case "platform":
				currentOrderBy = "meta_platform"
			case "library":
				currentOrderBy = "meta_library"
			}
		}
		if filter.AscDesc != nil {
			if *filter.AscDesc == "asc" {
				currentSortOrder = "ASC"
			} else if *filter.AscDesc == "desc" {
				currentSortOrder = "DESC"
			}
		}
		if filter.SubscribedMe != nil {
			if *filter.SubscribedMe == "yes" {
				filters = append(filters, "EXISTS (SELECT 1 FROM submission_notification_subscription sns WHERE sns.fk_submission_id = submission.id AND sns.fk_user_id = ?)")
			} else {
				filters = append(filters, "NOT EXISTS (SELECT 1 FROM submission_notification_subscription sns WHERE sns.fk_submission_id = submission.id AND sns.fk_user_id = ?)")
			}
			data = append(data, uid)
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.ExcludeLegacy {
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.UpdatedByID != nil {
			filters = append(filters, "(updater.id = ?)")
			data = append(data, *filter.UpdatedByID)
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
		if filter.IsContentChange != nil {
			masterFilters = append(masterFilters, "(1 = 0)") // content-change applies to submissions only
			if *filter.IsContentChange == "yes" {
				filters = append(filters, "(meta.game_exists = true)")
			} else {
				filters = append(filters, "(meta.game_exists = false)")
			}

		}
		if filter.IsFrozen != nil {
			if *filter.IsFrozen == "no" {
				filters = append(filters, "(submission.frozen_at IS NULL)")
			} else if *filter.IsFrozen == "yes" {
				filters = append(filters, "(submission.frozen_at IS NOT NULL)")
			}
			masterFilters = append(masterFilters, "(1 = 0)") // exclude legacy results
		}
	}

	and := ""
	if len(filters) > 0 {
		and = " AND "
	}

	masterAnd := ""
	if len(masterFilters) > 0 {
		masterAnd = " AND "
	}

	predicates := strings.Join(filters, " AND ")
	submissionWhere := ` WHERE submission.deleted_at IS NULL` + and + predicates
	legacyWhere := ` WHERE 1` + masterAnd + strings.Join(masterFilters, " AND ")
	searchComments := filter != nil && filter.CommentPartial != nil
	if searchComments {
		// The comment predicate is in FROM, before the ordinary WHERE parameters.
		data = append([]interface{}{*filter.CommentPartial}, data...)
	}
	countFrom := submissionSearchFrom(predicates, searchComments)
	// Reuse expensive metadata/comment matching for the total on nonempty
	// pages. Simple counts stay separate because their narrow index scan is
	// cheaper than adding a window over a broad, unfiltered result set.
	includeCount := strings.Contains(predicates, "meta.") || searchComments
	finalQuery := submissionSearchPageQuery(predicates, submissionWhere, legacyWhere, currentOrderBy, currentSortOrder, includeCount, searchComments)

	finalData := make([]interface{}, 0)
	finalData = append(finalData, data...)
	unlimitedData := append(finalData, masterData...)
	finalData = append(unlimitedData, currentLimit, currentOffset)

	// Count on the caller's transaction: its own writes and REPEATABLE READ
	// snapshot must agree with the page. DISTINCT preserves grouped search
	// semantics even if historical data contains duplicate cache/meta rows.
	countingQuery := `SELECT (SELECT COUNT(DISTINCT submission.id)` + countFrom + submissionWhere +
		`) + (SELECT COUNT(*) FROM masterdb_game` + legacyWhere + `)`

	rows, err := dbs.Tx().QueryContext(dbs.Ctx(), finalQuery, finalData...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	result := make([]*types.ExtendedSubmission, 0)

	var submitterAvatar string
	var updaterAvatar string
	var assignedTestingUserIDs *string
	var assignedVerificationUserIDs *string
	var requestedChangesUserIDs *string
	var approvedUserIDs *string
	var verifiedUserIDs *string
	var distinctActions *string
	var frozenAt *time.Time
	var counter int64
	// Reuse the destination slice, including room for the optional total, when
	// decoding large pages. Each iteration still owns a new submission value.
	dest := make([]interface{}, 0, 34)

	for rows.Next() {
		s := &types.ExtendedSubmission{}
		dest = append(dest[:0],
			&s.SubmissionID,
			&s.SubmissionLevel,
			&s.SubmitterID, &s.SubmitterUsername, &submitterAvatar,
			&s.UpdaterID, &s.UpdaterUsername, &updaterAvatar,
			&s.FileID, &s.OriginalFilename, &s.CurrentFilename, &s.Size,
			&s.UploadedAt, &s.UpdatedAt, &s.LastUploaderID,
			&s.CurationTitle, &s.CurationAlternateTitles, &s.CurationPlatform, &s.CurationLaunchCommand, &s.CurationLibrary, &s.CurationExtreme,
			&s.BotAction,
			&s.FileCount,
			&assignedTestingUserIDs, &assignedVerificationUserIDs, &requestedChangesUserIDs, &approvedUserIDs, &verifiedUserIDs,
			&distinctActions, &s.GameExists, &frozenAt, &s.ShouldAutofreeze, &s.GameUUID)
		if includeCount {
			dest = append(dest, &counter)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, 0, err
		}
		s.SubmitterAvatarURL = utils.FormatAvatarURL(s.SubmitterID, submitterAvatar)
		s.UpdaterAvatarURL = utils.FormatAvatarURL(s.UpdaterID, updaterAvatar)
		if frozenAt != nil {
			s.IsFrozen = true
		}

		s.AssignedTestingUserIDs = []int64{}
		if assignedTestingUserIDs != nil && len(*assignedTestingUserIDs) > 0 {
			userIDs := strings.Split(*assignedTestingUserIDs, ",")
			for _, userID := range userIDs {
				uid, err := strconv.ParseInt(userID, 10, 64)
				if err != nil {
					return nil, 0, err
				}
				s.AssignedTestingUserIDs = append(s.AssignedTestingUserIDs, uid)
			}
		}

		s.AssignedVerificationUserIDs = []int64{}
		if assignedVerificationUserIDs != nil && len(*assignedVerificationUserIDs) > 0 {
			userIDs := strings.Split(*assignedVerificationUserIDs, ",")
			for _, userID := range userIDs {
				uid, err := strconv.ParseInt(userID, 10, 64)
				if err != nil {
					return nil, 0, err
				}
				s.AssignedVerificationUserIDs = append(s.AssignedVerificationUserIDs, uid)
			}
		}

		s.RequestedChangesUserIDs = []int64{}
		if requestedChangesUserIDs != nil && len(*requestedChangesUserIDs) > 0 {
			userIDs := strings.Split(*requestedChangesUserIDs, ",")
			for _, userID := range userIDs {
				uid, err := strconv.ParseInt(userID, 10, 64)
				if err != nil {
					return nil, 0, err
				}
				s.RequestedChangesUserIDs = append(s.RequestedChangesUserIDs, uid)
			}
		}

		s.ApprovedUserIDs = []int64{}
		if approvedUserIDs != nil && len(*approvedUserIDs) > 0 {
			userIDs := strings.Split(*approvedUserIDs, ",")
			for _, userID := range userIDs {
				uid, err := strconv.ParseInt(userID, 10, 64)
				if err != nil {
					return nil, 0, err
				}
				s.ApprovedUserIDs = append(s.ApprovedUserIDs, uid)
			}
		}

		s.VerifiedUserIDs = []int64{}
		if verifiedUserIDs != nil && len(*verifiedUserIDs) > 0 {
			userIDs := strings.Split(*verifiedUserIDs, ",")
			for _, userID := range userIDs {
				uid, err := strconv.ParseInt(userID, 10, 64)
				if err != nil {
					return nil, 0, err
				}
				s.VerifiedUserIDs = append(s.VerifiedUserIDs, uid)
			}
		}

		s.DistinctActions = []string{}
		if distinctActions != nil && len(*distinctActions) > 0 {
			s.DistinctActions = append(s.DistinctActions, strings.Split(*distinctActions, ",")...)
		}

		result = append(result, s)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	// With no offset, an empty page proves there are no matches. Past the end
	// of a later page, however, zero rows does not imply a zero total.
	if len(result) == 0 && currentOffset == 0 {
		return result, 0, nil
	}
	// A later empty page carries no window value: count on the same transaction.
	if includeCount && len(result) > 0 {
		return result, counter, nil
	}
	if err := dbs.Tx().QueryRowContext(dbs.Ctx(), countingQuery, unlimitedData...).Scan(&counter); err != nil {
		return nil, 0, err
	}

	return result, counter, nil
}

func addMultifilter(tableName string, masterTableName *string, filterContents string, filters, masterFilters []string, data, masterData []interface{}) ([]string, []string, []interface{}, []interface{}) {
	substrings := strings.Split(filterContents, ",")
	trimmed := make([]string, 0, len(substrings))
	for _, ss := range substrings {
		trimmed = append(trimmed, strings.TrimSpace(ss))
	}
	include := make([]string, 0, len(substrings))
	exclude := make([]string, 0, len(substrings))
	for _, s := range trimmed {
		if len(s) == 0 {
			continue
		}
		if s[0] == '!' {
			exclude = append(exclude, s[1:])
		} else {
			include = append(include, s)
		}
	}

	if len(include) > 0 {
		includePlaceholder := `(` + tableName + ` LIKE ?)`
		filters = append(filters, `(`+includePlaceholder+strings.Repeat(` OR `+includePlaceholder, len(include)-1)+`)`)

		if masterTableName != nil {
			masterIncludePlaceholder := `(` + *masterTableName + ` LIKE ?)`
			masterFilters = append(masterFilters, `(`+masterIncludePlaceholder+strings.Repeat(` OR `+masterIncludePlaceholder, len(include)-1)+`)`)
		}
	}

	if len(exclude) > 0 {
		excludePlaceholder := `(` + tableName + ` NOT LIKE ?)`
		filters = append(filters, `(`+excludePlaceholder+strings.Repeat(` AND `+excludePlaceholder, len(exclude)-1)+`)`)

		if masterTableName != nil {
			masterExcludePlaceholder := `(` + *masterTableName + ` NOT LIKE ?)`
			masterFilters = append(masterFilters, `(`+masterExcludePlaceholder+strings.Repeat(` AND `+masterExcludePlaceholder, len(exclude)-1)+`)`)
		}
	}

	for _, s := range include {
		data = append(data, utils.FormatLike(s))
		if masterTableName != nil {
			masterData = append(masterData, utils.FormatLike(s))
		}
	}
	for _, s := range exclude {
		data = append(data, utils.FormatLike(s))
		if masterTableName != nil {
			masterData = append(masterData, utils.FormatLike(s))
		}
	}

	return filters, masterFilters, data, masterData
}

func magicAnd(a []string) string {
	if len(a) > 0 {
		return " AND "
	}
	return ""
}
