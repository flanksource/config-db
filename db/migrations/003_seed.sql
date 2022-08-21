-- +goose Up
-- +goose StatementBegin
---

CREATE TABLE config_scrapers (
  id UUID DEFAULT generate_ulid() PRIMARY KEY,
  description TEXT NULL,
  scraper_type text NOT NULL,
  spec jsonb,
  created_at timestamp NOT NULL DEFAULT now(),
  updated_at timestamp NOT NULL DEFAULT now()
);

CREATE TABLE config_items (
  id UUID DEFAULT generate_ulid() PRIMARY KEY,
  scraper_id UUID NULL,
  config_type text NOT NULL, -- The standardized type e.g. Subnet, Network, Host, etc. that applies across platforms
  external_id text[],
  external_type text, -- The external type, that combined with external id forms the natural id
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
  FOREIGN KEY (scraper_id) REFERENCES config_scrapers(id)
);

CREATE INDEX idx_config_items_external_id ON config_items USING GIN(external_id);

CREATE TABLE config_relationships(
  config_id UUID NOT NULL,
  related_id UUID NOT NULL,
  property text NULL, -- The component property name that this config is for
  created_at timestamp NOT NULL DEFAULT now(),
  updated_at timestamp NOT NULL DEFAULT now(),
  deleted_at TIMESTAMP DEFAULT NULL,
  selector_id text, -- hash of the selector from the components
  FOREIGN KEY(config_id) REFERENCES config_items(id),
  FOREIGN KEY(related_id) REFERENCES config_items(id),
	UNIQUE (related_id,config_id,selector_id)
);

CREATE TABLE config_changes (
  id UUID DEFAULT generate_ulid() PRIMARY KEY,
  config_id UUID NOT NULL,
  external_change_id text NULL,
  change_type text NULL,
  severity text  NULL,
  source text  NULL,
  summary text,
  patches jsonb null,
  details jsonb null,
  created_at timestamp NOT NULL DEFAULT now(),
  FOREIGN KEY (config_id) REFERENCES config_items(id)
);


CREATE TABLE config_analysis (
  id UUID DEFAULT generate_ulid() PRIMARY KEY,
  config_id uuid NOT NULL,
  analyzer text NOT NULL,
  analysis_type TEXT NULL, -- e.g. "cost", "security" or "performance"
  severity TEXT NULL, -- e.g. "low", "medium", "high"
  summary text,
  status text,
  message text,
  analysis jsonb null,
  first_observed timestamp NOT NULL DEFAULT now(),
  last_observed timestamp,
  FOREIGN KEY (config_id) REFERENCES config_items(id)
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
-- +goose Down
DROP TABLE saved_query;
DROP TABLE config_analysis;
DROP TABLE config_changes;
DROP TABLE config_relationships;
DROP TABLE config_items;
DROP TABLE config_scrapers;
