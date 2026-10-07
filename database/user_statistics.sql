-- Aggregate action IDs before joining names; the comment statistics index
-- covers that scan. DISTINCT counts preserve search semantics for duplicate
-- historical cache rows. Roles use exact matching, like constants.IsStaff.
WITH submitted AS (
    SELECT f.fk_user_id AS uid,
        COUNT(DISTINCT s.id) AS total,
        COUNT(DISTINCT CASE WHEN sc.bot_action = 'approve' THEN s.id END) AS happy,
        COUNT(DISTINCT CASE WHEN sc.bot_action = 'request-changes' THEN s.id END) AS unhappy,
        COUNT(DISTINCT CASE WHEN sc.active_requested_changes_ids IS NOT NULL THEN s.id END) AS requested,
        COUNT(DISTINCT CASE WHEN sc.active_approved_ids IS NOT NULL AND sc.active_verified_ids IS NULL
            AND sc.distinct_actions NOT REGEXP 'reject|mark-added' THEN s.id END) AS approved,
        COUNT(DISTINCT CASE WHEN sc.active_verified_ids IS NOT NULL
            AND sc.distinct_actions NOT REGEXP 'reject|mark-added' THEN s.id END) AS verified,
        COUNT(DISTINCT CASE WHEN sc.distinct_actions REGEXP 'mark-added'
            AND sc.distinct_actions NOT REGEXP 'reject' THEN s.id END) AS added,
        COUNT(DISTINCT CASE WHEN sc.distinct_actions REGEXP 'reject' THEN s.id END) AS rejected
    FROM submission s
    JOIN submission_cache sc ON sc.fk_submission_id = s.id
    JOIN submission_file f ON f.id = sc.fk_oldest_file_id
    WHERE s.deleted_at IS NULL
    GROUP BY f.fk_user_id
), actions AS (
    SELECT x.uid, MAX(x.last_activity) AS last_activity,
        SUM(CASE WHEN a.name = 'comment' THEN x.n ELSE 0 END) AS commented,
        SUM(CASE WHEN a.name = 'request-changes' THEN x.n ELSE 0 END) AS requested,
        SUM(CASE WHEN a.name = 'approve' THEN x.n ELSE 0 END) AS approved,
        SUM(CASE WHEN a.name = 'verify' THEN x.n ELSE 0 END) AS verified,
        SUM(CASE WHEN a.name = 'mark-added' THEN x.n ELSE 0 END) AS added,
        SUM(CASE WHEN a.name = 'reject' THEN x.n ELSE 0 END) AS rejected
    FROM (
        SELECT c.fk_user_id AS uid, c.fk_action_id AS aid,
            COUNT(*) AS n, MAX(c.created_at) AS last_activity
        FROM comment c
        WHERE c.deleted_at IS NULL
        GROUP BY c.fk_user_id, c.fk_action_id
    ) x
    LEFT JOIN action a ON a.id = x.aid
    GROUP BY x.uid
), roles AS (
    SELECT ur.fk_uid AS uid,
        MAX(BINARY r.name IN (/*STAFF_ROLES*/)) AS staff,
        MAX(BINARY r.name = ?) AS trial
    FROM discord_user_role ur JOIN discord_role r ON r.id = ur.fk_rid
    GROUP BY ur.fk_uid
)
SELECT u.id, u.username, COALESCE(r.staff, 0), COALESCE(r.trial, 0), a.last_activity,
    COALESCE(a.commented, 0), COALESCE(a.requested, 0), COALESCE(a.approved, 0),
    COALESCE(a.verified, 0), COALESCE(a.added, 0), COALESCE(a.rejected, 0),
    COALESCE(s.total, 0), COALESCE(s.happy, 0), COALESCE(s.unhappy, 0),
    COALESCE(s.requested, 0), COALESCE(s.approved, 0), COALESCE(s.verified, 0),
    COALESCE(s.added, 0), COALESCE(s.rejected, 0)
FROM discord_user u
LEFT JOIN submitted s ON s.uid = u.id
LEFT JOIN actions a ON a.uid = u.id
LEFT JOIN roles r ON r.uid = u.id
ORDER BY u.id
