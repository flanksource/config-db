-- +goose Up

ALTER TABLE config_items ADD COLUMN parent_id UUID DEFAULT NULL;
ALTER TABLE config_items ADD COLUMN path text DEFAULT NULL;
