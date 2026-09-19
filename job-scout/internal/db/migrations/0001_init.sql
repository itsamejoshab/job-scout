-- Initial schema, ported from the original Alembic migration.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'jobsource') THEN
        CREATE TYPE jobsource AS ENUM ('LINKEDIN', 'INDEED');
    END IF;
END$$;

CREATE TABLE IF NOT EXISTS jobs (
    id          SERIAL PRIMARY KEY,
    job_source  jobsource DEFAULT 'LINKEDIN',
    title       VARCHAR(100) NOT NULL,
    company     VARCHAR(100) NOT NULL,
    description TEXT,
    location    VARCHAR(100) NOT NULL,
    date        TIMESTAMP DEFAULT now(),
    job_url     VARCHAR(250) NOT NULL,
    created_at  TIMESTAMP DEFAULT now(),
    updated_at  TIMESTAMP DEFAULT now(),
    new         BOOLEAN NOT NULL DEFAULT TRUE,
    duplicate   BOOLEAN NOT NULL DEFAULT FALSE,
    relevant    BOOLEAN NOT NULL DEFAULT FALSE,
    promising   BOOLEAN NOT NULL DEFAULT FALSE,
    notified    BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS ix_jobs_id ON jobs (id);

CREATE TABLE IF NOT EXISTS search_settings (
    id                 SERIAL PRIMARY KEY,
    desc_include_words JSON NOT NULL,
    desc_exclude_words JSON NOT NULL,
    title_include      JSON NOT NULL,
    title_exclude      JSON NOT NULL,
    company_exclude    JSON NOT NULL,
    created_at         TIMESTAMP DEFAULT now(),
    updated_at         TIMESTAMP DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ix_search_settings_id ON search_settings (id);

CREATE TABLE IF NOT EXISTS scraper_settings (
    id              SERIAL PRIMARY KEY,
    job_source      jobsource,
    search_queries  JSON NOT NULL,
    global_searches JSON NOT NULL DEFAULT '[]',
    timespan_code   VARCHAR(100) NOT NULL,
    pages_to_scrape INTEGER NOT NULL,
    rounds          INTEGER NOT NULL,
    created_at      TIMESTAMP DEFAULT now(),
    updated_at      TIMESTAMP DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ix_scraper_settings_id ON scraper_settings (id);
