DROP VIEW IF EXISTS configs;

CREATE or REPLACE VIEW configs AS
  SELECT
    ci.id,
    ci.scraper_id,
    ci.config_type,
    ci.external_id,
    ci.external_type,
    ci.name,
    ci.namespace,
    ci.description,
    ci.source,
    ci.tags,
    ci.created_by,
    ci.created_at,
    ci.updated_at,
    ci.deleted_at,
    ci.cost_per_minute,
    ci.cost_total_1d,
    ci.cost_total_7d,
    ci.cost_total_30d,
    analysis,
    changes
  FROM config_items as ci
    full join (
      SELECT config_id,
        json_agg(json_build_object('analyzer',analyzer,'analysis_type',analysis_type,'severity',severity)) as analysis
      FROM config_analysis
      GROUP BY  config_id
    ) as ca on ca.config_id = ci.id
    full join (
      SELECT config_id,
        json_agg(total) as changes
      FROM
      (SELECT config_id,json_build_object('change_type',change_type, 'severity', severity, 'total', count(*)) as total FROM config_changes GROUP BY config_id, change_type, severity) as config_change_types
      GROUP BY  config_id
    ) as cc on cc.config_id = ci.id;


CREATE or REPLACE VIEW config_names AS
  SELECT id, config_type, external_id, name FROM config_items;

CREATE or REPLACE VIEW config_types AS
  SELECT DISTINCT config_type FROM config_items;

CREATE or REPLACE VIEW analyzer_types AS
  SELECT DISTINCT analyzer FROM config_analysis;

CREATE or REPLACE VIEW analysis_types AS
  SELECT DISTINCT analysis_type FROM config_analysis;

CREATE or REPLACE VIEW change_types AS
  SELECT DISTINCT change_type FROM config_changes;
