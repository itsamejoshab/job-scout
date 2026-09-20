-- Optional notification delivery. Existing installs keep notifications on.
-- Delivery still requires WEBHOOK_BASE and WEBHOOK_ID at runtime.

ALTER TABLE search_settings
    ADD COLUMN IF NOT EXISTS notifications_enabled BOOLEAN NOT NULL DEFAULT TRUE;
