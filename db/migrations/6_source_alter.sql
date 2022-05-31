-- +goose Up
-- +goose StatementBegin
---
ALTER TABLE config_item ADD source text;

-- +goose StatementEnd