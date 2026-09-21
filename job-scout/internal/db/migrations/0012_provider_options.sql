-- Provider-specific scraper options (Indeed country/radius/jobType/etc.).
ALTER TABLE scraper_settings
    ADD COLUMN IF NOT EXISTS provider_options JSON NOT NULL DEFAULT '{}'::json;
