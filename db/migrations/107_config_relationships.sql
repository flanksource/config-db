-- +goose Up

CREATE TABLE IF NOT EXISTS config_relationships (
  parent_id UUID NOT NULL,
  child_id UUID NOT NULL,
  relation TEXT NOT NULL
);
