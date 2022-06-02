-- +goose Up
alter table config_item add if not exists tags jsonb null;

-- +goose Down
alter table config_item drop column if exists tags;
