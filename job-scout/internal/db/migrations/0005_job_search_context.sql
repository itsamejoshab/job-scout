-- Preserve the search query that first introduced each job for diagnostics.

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS search_context TEXT NOT NULL DEFAULT '';
