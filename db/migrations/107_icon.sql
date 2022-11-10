-- +goose Up

ALTER TABLE config_items ADD COLUMN icon TEXT NULL;
