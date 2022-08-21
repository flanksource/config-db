-- +goose Up
-- +goose StatementBegin
---
CREATE or REPLACE VIEW configs AS
  SELECT
    ci.*,
    analysis,
    changes
  FROM config_items as ci
    full join (
      SELECT config_id,
        array_agg(analyzer) as analysis
      FROM config_analysis
      GROUP BY  config_id
    ) as ca on ca.config_id = ci.id
    full join (
      SELECT config_id,
        json_agg(total) as changes
      FROM
      (SELECT config_id, json_build_object(change_type, count(*)) as total FROM config_changes GROUP BY config_id, change_type) as config_change_types
      GROUP BY  config_id
    ) as cc on cc.config_id = ci.id;

-- +goose StatementEnd
