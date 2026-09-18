-- Enforce the singleton settings assumptions used by settings reads.

DELETE FROM scraper_settings
WHERE job_source IS NULL;

DELETE FROM scraper_settings AS extra
USING scraper_settings AS keep
WHERE extra.job_source = keep.job_source
  AND extra.id > keep.id;

DELETE FROM search_settings AS extra
USING search_settings AS keep
WHERE extra.id > keep.id;

ALTER TABLE scraper_settings
    ALTER COLUMN job_source SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS scraper_settings_job_source_key
    ON scraper_settings (job_source);

CREATE UNIQUE INDEX IF NOT EXISTS search_settings_singleton_key
    ON search_settings ((TRUE));

CREATE INDEX IF NOT EXISTS jobs_job_source_state_idx
    ON jobs (job_source, state);

CREATE INDEX IF NOT EXISTS jobs_created_at_idx
    ON jobs (created_at);
