ALTER TABLE apps ADD COLUMN is_primary INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1));
ALTER TABLE app_runs ADD COLUMN terminal_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE app_runs ADD COLUMN ownership TEXT NOT NULL DEFAULT 'managed' CHECK (ownership IN ('managed', 'attached'));
ALTER TABLE app_runs ADD COLUMN discovery_source TEXT NOT NULL DEFAULT 'manual' CHECK (discovery_source IN ('manual', 'detected', 'agent'));
ALTER TABLE app_runs ADD COLUMN detected_port INTEGER CHECK (detected_port IS NULL OR detected_port BETWEEN 1 AND 65535);
ALTER TABLE app_runs ADD COLUMN last_verified_at TEXT;
UPDATE apps SET is_primary = 0 WHERE id IN (
    SELECT duplicate.id FROM apps AS duplicate
    WHERE duplicate.is_primary = 1
      AND EXISTS (
          SELECT 1 FROM apps AS earlier
          WHERE earlier.project_id = duplicate.project_id
            AND earlier.is_primary = 1
            AND (earlier.created_at < duplicate.created_at
                 OR (earlier.created_at = duplicate.created_at AND earlier.id < duplicate.id))
      )
);
UPDATE apps SET is_primary = 1 WHERE id IN (
    SELECT (SELECT candidate.id FROM apps AS candidate
            WHERE candidate.project_id = grouped.project_id
            ORDER BY candidate.created_at, candidate.id LIMIT 1)
    FROM apps AS grouped
    GROUP BY grouped.project_id
    HAVING MAX(grouped.is_primary) = 0
);
CREATE UNIQUE INDEX IF NOT EXISTS apps_primary_project_idx ON apps (project_id) WHERE is_primary = 1;
CREATE INDEX IF NOT EXISTS app_runs_terminal_idx ON app_runs (terminal_session_id) WHERE terminal_session_id <> '';
