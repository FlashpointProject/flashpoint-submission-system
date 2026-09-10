-- Read-only, identifier-only diagnostics. These are observations, not repairs.
-- Metadata UUIDs remain text; missing historical parents remain historical facts.
SELECT 'metadata_uuid_noncanonical' AS finding, id::text AS row_id,
       NULL::text AS event_area, NULL::text AS event_operation, NULL::text AS parent_id
FROM curation_meta
WHERE uuid IS NOT NULL AND uuid<>''
  AND uuid !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
UNION ALL
SELECT 'activity_comment_id_missing_parent', e.id::text,
       e.event_area, e.event_operation, e.event_data->>'comment_id'
FROM activity_events e
WHERE e.event_data->>'comment_id' ~ '^[0-9]+$'
  AND NOT EXISTS(SELECT 1 FROM comment c
    WHERE c.id::numeric=CASE WHEN e.event_data->>'comment_id' ~ '^[0-9]+$'
      THEN (e.event_data->>'comment_id')::numeric END)
ORDER BY finding,row_id;
