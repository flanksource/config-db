-- +goose Up

ALTER TABLE config_items ADD COLUMN cost_per_minute numeric(16,4) NULL;
ALTER TABLE config_items ADD COLUMN cost_total_1d numeric(16,4) NULL;
ALTER TABLE config_items ADD COLUMN cost_total_7d numeric(16,4) NULL;
ALTER TABLE config_items ADD COLUMN cost_total_30d numeric(16,4) NULL;
