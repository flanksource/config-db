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
  duration_millis int,
  resource_type text,
  resource_id text,
  status text,
  time_start timestamp,
  time_end timestamp NULL,
  created_at timestamp NOT NULL DEFAULT now()
);

-- +goose StatementEnd
