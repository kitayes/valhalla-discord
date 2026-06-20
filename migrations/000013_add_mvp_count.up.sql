-- Migration 000013: Add MVP/SVP counters to players table
ALTER TABLE players ADD COLUMN IF NOT EXISTS mvp_count INT DEFAULT 0;
ALTER TABLE players ADD COLUMN IF NOT EXISTS svp_count INT DEFAULT 0;