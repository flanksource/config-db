-- +goose Up
-- +goose StatementBegin
---
ALTER TABLE config_item ADD source text;

-- +goose StatementEnd

-- +goose Down
alter table config_item drop column if exists source;
