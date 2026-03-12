package db

import (
	"encoding/json"

	"github.com/flanksource/duty/context"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/uuid"
)

func init() {
	context.CelEnvFuncs["db.external_users"] = externalUsersCEL(false)
	context.CelEnvFuncs["db.external_users_all"] = externalUsersCEL(true)
	context.CelEnvFuncs["db.external_groups"] = externalGroupsCEL(false)
	context.CelEnvFuncs["db.external_groups_all"] = externalGroupsCEL(true)
	context.CelEnvFuncs["db.external_roles"] = externalRolesCEL(false)
	context.CelEnvFuncs["db.external_roles_all"] = externalRolesCEL(true)
}

func externalUsersCEL(includeDeleted bool) func(context.Context) cel.EnvOption {
	suffix := ""
	if includeDeleted {
		suffix = "_all"
	}
	return func(ctx context.Context) cel.EnvOption {
		return cel.Function("db.external_users"+suffix,
			cel.Overload("db_external_users"+suffix+"_string",
				[]*cel.Type{cel.StringType},
				cel.ListType(cel.DynType),
				cel.UnaryBinding(func(arg ref.Val) ref.Val {
					scraperID, err := uuid.Parse(arg.Value().(string))
					if err != nil {
						return types.WrapErr(err)
					}
					return queryEntities[dutyModels.ExternalUser](ctx, "external_users", scraperID, includeDeleted)
				}),
			),
		)
	}
}

func externalGroupsCEL(includeDeleted bool) func(context.Context) cel.EnvOption {
	suffix := ""
	if includeDeleted {
		suffix = "_all"
	}
	return func(ctx context.Context) cel.EnvOption {
		return cel.Function("db.external_groups"+suffix,
			cel.Overload("db_external_groups"+suffix+"_string",
				[]*cel.Type{cel.StringType},
				cel.ListType(cel.DynType),
				cel.UnaryBinding(func(arg ref.Val) ref.Val {
					scraperID, err := uuid.Parse(arg.Value().(string))
					if err != nil {
						return types.WrapErr(err)
					}
					return queryEntities[dutyModels.ExternalGroup](ctx, "external_groups", scraperID, includeDeleted)
				}),
			),
		)
	}
}

func externalRolesCEL(includeDeleted bool) func(context.Context) cel.EnvOption {
	suffix := ""
	if includeDeleted {
		suffix = "_all"
	}
	return func(ctx context.Context) cel.EnvOption {
		return cel.Function("db.external_roles"+suffix,
			cel.Overload("db_external_roles"+suffix+"_string",
				[]*cel.Type{cel.StringType},
				cel.ListType(cel.DynType),
				cel.UnaryBinding(func(arg ref.Val) ref.Val {
					scraperID, err := uuid.Parse(arg.Value().(string))
					if err != nil {
						return types.WrapErr(err)
					}
					return queryEntities[dutyModels.ExternalRole](ctx, "external_roles", scraperID, includeDeleted)
				}),
			),
		)
	}
}

func queryEntities[T any](ctx context.Context, table string, scraperID uuid.UUID, includeDeleted bool) ref.Val {
	var rows []T
	q := ctx.DB().Table(table).Where("scraper_id = ?", scraperID)
	if !includeDeleted {
		q = q.Where("deleted_at IS NULL")
	}
	if err := q.Find(&rows).Error; err != nil {
		return types.WrapErr(err)
	}
	raw, _ := json.Marshal(rows)
	var result []any
	_ = json.Unmarshal(raw, &result)
	if result == nil {
		result = []any{}
	}
	return types.DefaultTypeAdapter.NativeToValue(result)
}
