-- +goose Up
-- +goose StatementBegin
---


CREATE TABLE saved_query (
  id UUID DEFAULT generate_ulid() PRIMARY KEY,
  icon TEXT NULL,
  description TEXT NULL,
  query text NOT NULL,
  columns jsonb null,
  created_by TEXT NULL,
  created_at timestamp NOT NULL DEFAULT now(),
  updated_at timestamp NOT NULL DEFAULT now()
);


-- +goose StatementEnd
