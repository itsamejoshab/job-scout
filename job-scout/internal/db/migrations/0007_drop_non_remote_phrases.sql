-- Drop remote-lie filter lists; work-type search signals are no longer reliable.
ALTER TABLE search_settings DROP COLUMN IF EXISTS non_remote_phrases;
