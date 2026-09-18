-- Cadence flags for scrape ticks. UPDATE existing LinkedIn/Indeed rows
-- even when seed-once already inserted them.

ALTER TABLE scraper_settings ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE scraper_settings ADD COLUMN IF NOT EXISTS scrape_interval_seconds INTEGER NOT NULL DEFAULT 900;
ALTER TABLE scraper_settings ADD COLUMN IF NOT EXISTS last_scraped_at TIMESTAMPTZ NULL;
ALTER TABLE scraper_settings ADD COLUMN IF NOT EXISTS next_eligible_at TIMESTAMPTZ NULL;

UPDATE scraper_settings
SET enabled = TRUE, scrape_interval_seconds = 900
WHERE job_source = 'LINKEDIN';

UPDATE scraper_settings
SET enabled = FALSE
WHERE job_source = 'INDEED';
