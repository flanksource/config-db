-- +goose Up
-- +goose StatementBegin
---

ALTER TABLE config_item  ADD tags jsonb null;

-- +goose StatementEnd
