-- +goose Up
-- +goose StatementBegin
---


CREATE TABLE config_scraper (
  id UUID DEFAULT generate_ulid() PRIMARY KEY,
  description TEXT NULL,
  scraper_type text NOT NULL,
  spec jsonb,
  created_at timestamp NOT NULL DEFAULT now(),
  updated_at timestamp NOT NULL DEFAULT now()
);

CREATE TABLE config_item (
  id UUID DEFAULT generate_ulid() PRIMARY KEY,
  scraper_id UUID NULL,
  config_type text NOT NULL,
  external_id text,
  name text,
  namespace text,
  description text,
  account text,
  region text,
  zone text,
  network text,
  subnet text,
  config jsonb null,
  source TEXT null,
  tags jsonb null,
  created_at timestamp NOT NULL DEFAULT now(),
  updated_at timestamp NOT NULL DEFAULT now(),
  FOREIGN KEY (scraper_id) REFERENCES config_scraper(id)
);

CREATE TABLE config_relationships(
  config_id UUID NOT NULL,
  related_id UUID NOT NULL,
  property text NULL, -- The component property name that this config is for
  created_at timestamp NOT NULL DEFAULT now(),
  updated_at timestamp NOT NULL DEFAULT now(),
  deleted_at TIMESTAMP DEFAULT NULL,
  selector_id text, -- hash of the selector from the components
  FOREIGN KEY(config_id) REFERENCES config_item(id),
  FOREIGN KEY(related_id) REFERENCES config_item(id),
	UNIQUE (related_id,config_id,selector_id)
);

CREATE TABLE config_change (
  id UUID DEFAULT generate_ulid() PRIMARY KEY,
  config_id UUID NOT NULL,
  change_type text NOT NULL,
  summary text,
  patches jsonb null,
  created_at timestamp NOT NULL DEFAULT now(),
  FOREIGN KEY (config_id) REFERENCES config_item(id)
);

CREATE TABLE config_analysis (
  id UUID DEFAULT generate_ulid() PRIMARY KEY,
  config_id uuid NOT NULL,
  analyzer text NOT NULL,
  summary text,
  analysis_type text NOT NULL,
  analysis jsonb null,
  first_observed timestamp NOT NULL DEFAULT now(),
  last_observed timestamp,
  FOREIGN KEY (config_id) REFERENCES config_item(id)
);

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

-- INSERT INTO config_db_version (version_id,is_applied,tstamp) values ('3',true, now())


-- +goose StatementEnd
