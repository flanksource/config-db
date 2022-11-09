-- +goose Up

ALTER TABLE config_items ADD COLUMN path text DEFAULT NULL;
