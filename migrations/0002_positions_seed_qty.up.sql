-- Initial inventory granted at seed time is a reconcile baseline, not trade flow.
ALTER TABLE positions ADD COLUMN IF NOT EXISTS seed_qty BIGINT NOT NULL DEFAULT 0;
