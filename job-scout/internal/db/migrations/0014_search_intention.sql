-- Stamp each job with the work-type intent of the search that found it, and
-- store keyword lists used to reject remote/hybrid searches that look onsite.

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS search_intention TEXT NOT NULL DEFAULT 'onsite';

ALTER TABLE jobs
    DROP CONSTRAINT IF EXISTS jobs_search_intention_check;

ALTER TABLE jobs
    ADD CONSTRAINT jobs_search_intention_check
    CHECK (search_intention IN ('onsite', 'remote', 'hybrid', 'remote_hybrid'));

ALTER TABLE search_settings
    ADD COLUMN IF NOT EXISTS onsite_keywords JSON NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS remote_keywords JSON NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS hybrid_keywords JSON NOT NULL DEFAULT '[]';
