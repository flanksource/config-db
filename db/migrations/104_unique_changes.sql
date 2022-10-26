-- +goose Up

CREATE UNIQUE INDEX config_changes_unique on config_changes(config_id, external_change_id);
