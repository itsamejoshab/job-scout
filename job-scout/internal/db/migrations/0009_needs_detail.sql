ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_state_check;

ALTER TABLE jobs ADD CONSTRAINT jobs_state_check
    CHECK (state IN ('pending', 'rejected', 'needs_detail', 'ready', 'applied', 'dismissed'));
