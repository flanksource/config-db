-- +goose Up

CREATE TABLE IF NOT EXISTS config_item_relationships (
  parent_id UUID NOT NULL,
  child_id UUID NOT NULL,
  relation TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT now(),
  updated_at TIMESTAMP NOT NULL DEFAULT now(),
  deleted_at TIMESTAMP DEFAULT NULL,
  FOREIGN KEY (parent_id) REFERENCES config_items(id),
  FOREIGN KEY (child_id) REFERENCES config_items(id),
  UNIQUE (parent_id, child_id, relation)
);
