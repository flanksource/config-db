-- +goose Up

ALTER TABLE config_scrapers ADD COLUMN created_by UUID NULL;
ALTER TABLE config_items ADD COLUMN     created_by UUID NULL;
ALTER TABLE config_changes ADD COLUMN   IF NOT EXISTS created_by UUID NULL;
ALTER TABLE config_changes ADD COLUMN   IF NOT EXISTS external_created_by TEXT NULL;
