-- +goose Up
-- +goose StatementBegin
---

CREATE TABLE IF NOT EXISTS job_history (
  id UUID DEFAULT generate_ulid() PRIMARY KEY,
  name text,
  success_count int,
  error_count int,
  details jsonb,
  hostname text,
  time_taken_ms int,
  resource_type text,
  resource_id text,
  created_at timestamp NOT NULL DEFAULT now()
);

-- +goose StatementEnd
