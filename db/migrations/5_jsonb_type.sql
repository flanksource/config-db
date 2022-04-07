-- +goose Up
-- +goose StatementBegin
---

ALTER TABLE config_item  ALTER COLUMN config  SET DATA TYPE jsonb USING config::jsonb;
ALTER TABLE config_scraper  ALTER COLUMN spec  SET DATA TYPE jsonb USING spec::jsonb;
ALTER TABLE config_change  ALTER COLUMN patches  SET DATA TYPE jsonb USING patches::jsonb;
ALTER TABLE config_analysis  ALTER COLUMN analysis  SET DATA TYPE jsonb USING analysis::jsonb;

-- +goose StatementEnd
