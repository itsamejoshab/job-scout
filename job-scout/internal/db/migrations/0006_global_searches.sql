-- Replace hardcoded LinkedIn URLs with free-form global search strings.
ALTER TABLE scraper_settings ADD COLUMN IF NOT EXISTS global_searches JSON;
UPDATE scraper_settings SET global_searches = '[]'::json WHERE global_searches IS NULL;
ALTER TABLE scraper_settings ALTER COLUMN global_searches SET DEFAULT '[]'::json;
ALTER TABLE scraper_settings ALTER COLUMN global_searches SET NOT NULL;
ALTER TABLE scraper_settings DROP COLUMN IF EXISTS hardcoded_urls;
