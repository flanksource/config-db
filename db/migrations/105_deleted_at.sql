-- +goose Up

ALTER TABLE config_items ADD COLUMN deleted_at TIMESTAMP DEFAULT NULL;
