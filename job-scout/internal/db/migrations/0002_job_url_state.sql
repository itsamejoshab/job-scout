-- Distinct job_url identity and notify-facing state columns.

ALTER TABLE jobs ALTER COLUMN job_url TYPE TEXT;

DELETE FROM jobs AS extra
    USING jobs AS keep
WHERE extra.job_url = keep.job_url
  AND extra.id > keep.id;

ALTER TABLE jobs ADD COLUMN IF NOT EXISTS state TEXT;
UPDATE jobs SET state = 'pending' WHERE state IS NULL;
ALTER TABLE jobs ALTER COLUMN state SET DEFAULT 'pending';
ALTER TABLE jobs ALTER COLUMN state SET NOT NULL;

ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_state_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_state_check
    CHECK (state IN ('pending', 'rejected', 'eligible', 'notifying', 'notified'));

ALTER TABLE jobs ADD COLUMN IF NOT EXISTS reject_reason TEXT;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS is_remote BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS detail_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS state_changed_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE UNIQUE INDEX IF NOT EXISTS jobs_job_url_key ON jobs (job_url);
