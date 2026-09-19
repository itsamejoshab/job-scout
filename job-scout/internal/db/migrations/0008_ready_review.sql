ALTER TABLE jobs ADD COLUMN IF NOT EXISTS notified_at TIMESTAMPTZ;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS notify_claimed_at TIMESTAMPTZ;

ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_state_check;

UPDATE jobs
SET notified_at = CASE
        WHEN state = 'notified' THEN state_changed_at
        ELSE NULL
    END,
    notify_claimed_at = NULL,
    state = CASE
        WHEN state IN ('eligible', 'notifying', 'notified') THEN 'ready'
        ELSE state
    END;

ALTER TABLE jobs ADD CONSTRAINT jobs_state_check
    CHECK (state IN ('pending', 'rejected', 'ready', 'applied', 'dismissed'));

CREATE INDEX IF NOT EXISTS jobs_notify_claim_idx
    ON jobs (state, notified_at, notify_claimed_at, created_at, id);
