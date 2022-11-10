-- +goose Up

ALTER TABLE config_items ADD COLUMN parent_id UUID NULL;
ALTER TABLE config_items ADD FOREIGN KEY (parent_id) REFERENCES config_items(id);
ALTER TABLE config_items ADD COLUMN path TEXT NULL;
