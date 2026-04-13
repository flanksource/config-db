package extract

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/samber/lo"
)

type Resolver interface {
	SyncExternalUsers(users []models.ExternalUser, scraperID *uuid.UUID) ([]models.ExternalUser, map[uuid.UUID]uuid.UUID, error)
	SyncExternalGroups(groups []models.ExternalGroup, scraperID *uuid.UUID) ([]models.ExternalGroup, map[uuid.UUID]uuid.UUID, error)
	SyncExternalRoles(roles []models.ExternalRole, scraperID *uuid.UUID) ([]models.ExternalRole, error)

	FindUserIDByAliases(aliases []string) (*uuid.UUID, error)
	FindRoleIDByAliases(aliases []string) (*uuid.UUID, error)
	FindGroupIDByAliases(aliases []string) (*uuid.UUID, error)
	FindConfigIDByExternalID(ext v1.ExternalID) (uuid.UUID, error)
}

type ExtractedConfig struct {
	Config             any                          `json:"config,omitempty"`
	Changes            []v1.ChangeResult            `json:"changes,omitempty"`
	Analysis           []v1.AnalysisResult          `json:"analysis,omitempty"`
	AccessLogs         []v1.ExternalConfigAccessLog `json:"access_logs,omitempty"`
	ConfigAccess       []v1.ExternalConfigAccess    `json:"config_access,omitempty"`
	ExternalUsers      []models.ExternalUser        `json:"external_users,omitempty"`
	ExternalGroups     []models.ExternalGroup       `json:"external_groups,omitempty"`
	ExternalUserGroups []models.ExternalUserGroup   `json:"external_user_groups,omitempty"`
	ExternalRoles      []models.ExternalRole        `json:"external_roles,omitempty"`
	Summary            ExtractionSummary            `json:"summary,omitempty"`
	Warnings           []v1.Warning                 `json:"warnings,omitempty"`

	// Transform context for diagnostic warnings — not serialized.
	transformInput  any    `json:"-"`
	transformOutput any    `json:"-"`
	transformExpr   string `json:"-"`
}

func (e *ExtractedConfig) SetTransformContext(input, output any, expr string) {
	e.transformInput = input
	e.transformOutput = output
	e.transformExpr = expr
}

func (e ExtractedConfig) HasEntities() bool {
	return len(e.ConfigAccess) > 0 || len(e.AccessLogs) > 0 ||
		len(e.ExternalUsers) > 0 || len(e.ExternalGroups) > 0 ||
		len(e.ExternalRoles) > 0 || len(e.ExternalUserGroups) > 0 ||
		len(e.Changes) > 0 || len(e.Analysis) > 0
}

func (e *ExtractedConfig) AddWarning(w v1.Warning) {
	if w.Input == nil {
		w.Input = e.transformInput
	}
	if w.Output == nil {
		w.Output = e.transformOutput
	}
	if w.Expr == "" {
		w.Expr = e.transformExpr
	}
	e.Warnings = append(e.Warnings, w)
}

func (e ExtractedConfig) Pretty() api.Text {
	t := clicky.Text("")
	type entry struct {
		label string
		count int
	}
	for _, e := range []entry{
		{"changes", len(e.Changes)},
		{"users", len(e.ExternalUsers)},
		{"groups", len(e.ExternalGroups)},
		{"roles", len(e.ExternalRoles)},
		{"user_groups", len(e.ExternalUserGroups)},
		{"access", len(e.ConfigAccess)},
		{"access_logs", len(e.AccessLogs)},
	} {
		if e.count == 0 {
			continue
		}
		t = t.AddText(e.label, "font-bold").Appendf("=%d ", e.count)
	}
	return t.NewLine().Add(e.Summary.Pretty())
}

func (e ExtractedConfig) Merge(other ExtractedConfig) ExtractedConfig {
	e.Changes = append(e.Changes, other.Changes...)
	e.AccessLogs = append(e.AccessLogs, other.AccessLogs...)
	e.Analysis = append(e.Analysis, other.Analysis...)
	e.ConfigAccess = append(e.ConfigAccess, other.ConfigAccess...)
	e.ExternalUsers = append(e.ExternalUsers, other.ExternalUsers...)
	e.ExternalUserGroups = append(e.ExternalUserGroups, other.ExternalUserGroups...)
	e.ExternalRoles = append(e.ExternalRoles, other.ExternalRoles...)
	e.ExternalGroups = append(e.ExternalGroups, other.ExternalGroups...)
	e.Summary = e.Summary.Merge(other.Summary)
	for _, w := range other.Warnings {
		e.AddWarning(w)
	}
	return e
}

type ExtractionSummary struct {
	Users        v1.EntitySummary[models.ExternalUser]  `json:"users,omitzero"`
	Groups       v1.EntitySummary[models.ExternalGroup] `json:"groups,omitzero"`
	Roles        v1.EntitySummary[models.ExternalRole]  `json:"roles,omitzero"`
	ConfigAccess v1.EntitySummary[struct{}]             `json:"config_access,omitzero"`
	AccessLogs   v1.EntitySummary[struct{}]             `json:"access_logs,omitzero"`
	Changes      v1.EntitySummary[struct{}]             `json:"changes,omitzero"`
	Analysis     v1.EntitySummary[struct{}]             `json:"analysis,omitzero"`
}

func (e ExtractionSummary) IsEmpty() bool {
	return e.Users.IsEmpty() && e.Groups.IsEmpty() && e.Roles.IsEmpty() &&
		e.ConfigAccess.IsEmpty() && e.AccessLogs.IsEmpty() &&
		e.Changes.IsEmpty() && e.Analysis.IsEmpty()
}

func (e ExtractionSummary) Pretty() api.Text {
	t := clicky.Text("")
	type entitySummaryLike interface {
		IsEmpty() bool
		Pretty() api.Text
	}
	type entry struct {
		label   string
		summary entitySummaryLike
	}
	for _, e := range []entry{
		{"users", e.Users},
		{"groups", e.Groups},
		{"roles", e.Roles},
		{"config_access", e.ConfigAccess},
		{"access_logs", e.AccessLogs},
	} {
		if e.summary.IsEmpty() {
			continue
		}
		t = t.AddText(e.label, "font-bold").AddText("=").Add(e.summary.Pretty()).Space()
	}
	return t
}

func (e ExtractionSummary) Merge(other ExtractionSummary) ExtractionSummary {
	e.Users = e.Users.Merge(other.Users)
	e.Groups = e.Groups.Merge(other.Groups)
	e.Roles = e.Roles.Merge(other.Roles)
	e.ConfigAccess = e.ConfigAccess.Merge(other.ConfigAccess)
	e.AccessLogs = e.AccessLogs.Merge(other.AccessLogs)
	e.Analysis = e.Analysis.Merge(other.Analysis)
	e.Changes = e.Changes.Merge(other.Changes)
	return e
}

func findKey(m map[string]any, keys ...string) (any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v, true
		}
	}
	return nil, false
}

func unmarshalKey(m map[string]any, dest any, label string, keys ...string) error {
	v, ok := findKey(m, keys...)
	if !ok {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal %s: %w", label, err)
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("failed to unmarshal %s: %w", label, err)
	}
	return nil
}

func findStringKey(m map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

// ExtractConfigChangesFromConfig extracts config, changes, access logs, config access,
// external users, groups, user groups, and roles from the scraped config.
//
// When resolver is non-nil, entities are synced and aliases are resolved to UUIDs.
// When resolver is nil, only parsing and default-filling is performed.
// TransformContext holds diagnostic context from the transform that produced this config.
type TransformContext struct {
	Input any
	Expr  string
}

func ExtractConfigChangesFromConfig(resolver Resolver, scraperID *uuid.UUID, config any, tc ...TransformContext) (ExtractedConfig, error) {
	configMap, ok := config.(map[string]any)
	if !ok {
		return ExtractedConfig{}, errors.New("config is not a map")
	}

	var result ExtractedConfig
	if len(tc) > 0 {
		result.SetTransformContext(tc[0].Input, config, tc[0].Expr)
	}

	if eConf, ok := configMap["config"]; ok {
		result.Config = eConf
	}

	sanitizeConfigIDFields(configMap)

	type field struct {
		dest  any
		label string
		keys  []string
	}
	fields := []field{
		{&result.Changes, "changes", []string{"changes"}},
		{&result.Analysis, "analysis", []string{"analysis"}},
		{&result.AccessLogs, "access logs", []string{"access_logs", "logs"}},
		{&result.ConfigAccess, "config access", []string{"config_access", "access"}},
		{&result.ExternalUsers, "external users", []string{"external_users", "users"}},
		{&result.ExternalGroups, "external groups", []string{"external_groups", "groups"}},
		{&result.ExternalUserGroups, "external user groups", []string{"external_user_groups", "user_groups"}},
		{&result.ExternalRoles, "external roles", []string{"external_roles", "roles"}},
	}

	for _, f := range fields {
		if err := unmarshalKey(configMap, f.dest, f.label, f.keys...); err != nil {
			return ExtractedConfig{}, err
		}
	}

	result.Summary.Changes.Scraped = len(result.Changes)
	result.Summary.Analysis.Scraped = len(result.Analysis)

	expandConfigAccessShorthand(configMap, result.ConfigAccess)
	expandAccessLogShorthand(configMap, result.AccessLogs)
	applyConfigRefDefaults(configMap, &result)
	validateConfigRefs(&result)

	if resolver != nil {
		if err := SyncEntities(resolver, scraperID, configMap, &result); err != nil {
			return result, err
		}
		if err := ResolveAccess(resolver, scraperID, &result); err != nil {
			return result, err
		}
	}

	return result, nil
}

func resolveUserGroupAliases(r Resolver, configMap map[string]any, result *ExtractedConfig) {
	rawUG, _ := findKey(configMap, "user_groups", "external_user_groups")
	rawItems := toMapSlice(rawUG)

	for i := range result.ExternalUserGroups {
		if i >= len(rawItems) {
			break
		}
		ug := &result.ExternalUserGroups[i]
		raw := rawItems[i]

		if ug.ExternalUserID == uuid.Nil {
			if alias, ok := raw["user"]; ok {
				aliases := toStringSlice(alias)
				if id, err := r.FindUserIDByAliases(aliases); err == nil && id != nil {
					ug.ExternalUserID = *id
				} else if len(aliases) > 0 {
					newID := uuid.New()
					ug.ExternalUserID = newID
					result.ExternalUsers = append(result.ExternalUsers, models.ExternalUser{
						ID:      newID,
						Name:    aliases[0],
						Aliases: aliases,
					})
				}
			}
		}

		if ug.ExternalGroupID == uuid.Nil {
			if alias, ok := raw["group"]; ok {
				aliases := toStringSlice(alias)
				if id, err := r.FindGroupIDByAliases(aliases); err == nil && id != nil {
					ug.ExternalGroupID = *id
				} else if len(aliases) > 0 {
					newID := uuid.New()
					ug.ExternalGroupID = newID
					result.ExternalGroups = append(result.ExternalGroups, models.ExternalGroup{
						ID:      newID,
						Name:    aliases[0],
						Aliases: aliases,
					})
				}
			}
		}
	}
}

// SyncEntities resolves entity IDs and maps user groups.
// Call this BEFORE persisting entities to DB.
func SyncEntities(r Resolver, scraperID *uuid.UUID, configMap map[string]any, result *ExtractedConfig) error {
	if configMap != nil {
		resolveUserGroupAliases(r, configMap, result)
	}
	resolvedUsers, userIDMap, err := r.SyncExternalUsers(result.ExternalUsers, scraperID)
	if err != nil {
		return fmt.Errorf("sync external users: %w", err)
	}
	result.Summary.Users.Scraped = len(result.ExternalUsers)
	result.ExternalUsers = resolvedUsers

	resolvedGroups, groupIDMap, err := r.SyncExternalGroups(result.ExternalGroups, scraperID)
	if err != nil {
		return fmt.Errorf("sync external groups: %w", err)
	}
	result.Summary.Groups.Scraped = len(result.ExternalGroups)
	result.ExternalGroups = resolvedGroups

	resolvedRoles, err := r.SyncExternalRoles(result.ExternalRoles, scraperID)
	if err != nil {
		return fmt.Errorf("sync external roles: %w", err)
	}
	result.Summary.Roles.Scraped = len(result.ExternalRoles)
	result.ExternalRoles = resolvedRoles

	var resolvedUserGroups []models.ExternalUserGroup
	for _, ug := range result.ExternalUserGroups {
		if savedID, ok := userIDMap[ug.ExternalUserID]; ok {
			ug.ExternalUserID = savedID
		}
		if savedID, ok := groupIDMap[ug.ExternalGroupID]; ok {
			ug.ExternalGroupID = savedID
		}
		if ug.ExternalUserID == uuid.Nil || ug.ExternalGroupID == uuid.Nil {
			continue
		}
		resolvedUserGroups = append(resolvedUserGroups, ug)
	}
	result.ExternalUserGroups = resolvedUserGroups

	return nil
}

// ResolveAccess resolves config_access aliases and access_log config IDs.
// Call this AFTER persisting entities to DB so FindUserIDByAliases can find them.
func ResolveAccess(r Resolver, scraperID *uuid.UUID, result *ExtractedConfig) error {
	result.Summary.ConfigAccess.Scraped = len(result.ConfigAccess)
	var resolvedAccesses []v1.ExternalConfigAccess
	for i := range result.ConfigAccess {
		ca := &result.ConfigAccess[i]

		if err := resolveConfigAccessAliases(r, ca, result); err != nil {
			return err
		}

		if ca.ConfigID == uuid.Nil && !ca.ConfigExternalID.IsEmpty() {
			configID, err := r.FindConfigIDByExternalID(ca.ConfigExternalID)
			if err != nil {
				return fmt.Errorf("find config for access: %w", err)
			}
			if configID == uuid.Nil {
				result.Summary.ConfigAccess.Skipped++
				continue
			}
			ca.ConfigID = configID
		}

		ca.ScraperID = lo.Ternary(ca.ScraperID == nil, scraperID, ca.ScraperID)

		if ca.ScraperID == nil && ca.ApplicationID == nil && ca.Source == nil {
			result.Summary.ConfigAccess.Skipped++
			continue
		}

		resolvedAccesses = append(resolvedAccesses, *ca)
	}
	result.ConfigAccess = resolvedAccesses

	result.Summary.AccessLogs.Scraped = len(result.AccessLogs)
	var resolvedLogs []v1.ExternalConfigAccessLog
	for i := range result.AccessLogs {
		al := &result.AccessLogs[i]

		if al.ExternalUserID == uuid.Nil && len(al.ExternalUserAliases) > 0 {
			id, err := r.FindUserIDByAliases(al.ExternalUserAliases)
			if err != nil {
				return fmt.Errorf("find user for access log: %w", err)
			}
			if id != nil {
				al.ExternalUserID = *id
			} else {
				newID := uuid.New()
				al.ExternalUserID = newID
				result.ExternalUsers = append(result.ExternalUsers, models.ExternalUser{
					ID:      newID,
					Name:    al.ExternalUserAliases[0],
					Aliases: al.ExternalUserAliases,
				})
			}
		}

		if al.ExternalUserID == uuid.Nil {
			result.Summary.AccessLogs.Skipped++
			continue
		}

		if al.ConfigID == uuid.Nil && !al.ConfigExternalID.IsEmpty() {
			configID, err := r.FindConfigIDByExternalID(al.ConfigExternalID)
			if err != nil {
				return fmt.Errorf("find config for access log: %w", err)
			}
			if configID == uuid.Nil {
				result.Summary.AccessLogs.Skipped++
				continue
			}
			al.ConfigID = configID
		}
		resolvedLogs = append(resolvedLogs, *al)
	}
	result.AccessLogs = resolvedLogs

	return nil
}

func resolveConfigAccessAliases(r Resolver, ca *v1.ExternalConfigAccess, result *ExtractedConfig) error {
	if ca.ExternalUserID == nil && len(ca.ExternalUserAliases) > 0 {
		id, err := r.FindUserIDByAliases(ca.ExternalUserAliases)
		if err != nil {
			return fmt.Errorf("find user by aliases: %w", err)
		}
		if id != nil {
			ca.ExternalUserID = id
		} else {
			newID := uuid.New()
			ca.ExternalUserID = &newID
			result.ExternalUsers = append(result.ExternalUsers, models.ExternalUser{
				ID:      newID,
				Name:    ca.ExternalUserAliases[0],
				Aliases: ca.ExternalUserAliases,
			})
		}
	}
	if ca.ExternalRoleID == nil && len(ca.ExternalRoleAliases) > 0 {
		id, err := r.FindRoleIDByAliases(ca.ExternalRoleAliases)
		if err != nil {
			return fmt.Errorf("find role by aliases: %w", err)
		}
		if id != nil {
			ca.ExternalRoleID = id
		} else {
			newID := uuid.New()
			ca.ExternalRoleID = &newID
			result.ExternalRoles = append(result.ExternalRoles, models.ExternalRole{
				ID:      newID,
				Name:    ca.ExternalRoleAliases[0],
				Aliases: ca.ExternalRoleAliases,
			})
		}
	}
	if ca.ExternalGroupID == nil && len(ca.ExternalGroupAliases) > 0 {
		id, err := r.FindGroupIDByAliases(ca.ExternalGroupAliases)
		if err != nil {
			return fmt.Errorf("find group by aliases: %w", err)
		}
		if id != nil {
			ca.ExternalGroupID = id
		} else {
			newID := uuid.New()
			ca.ExternalGroupID = &newID
			result.ExternalGroups = append(result.ExternalGroups, models.ExternalGroup{
				ID:      newID,
				Name:    ca.ExternalGroupAliases[0],
				Aliases: ca.ExternalGroupAliases,
			})
		}
	}
	return nil
}

func toMapSlice(v any) []map[string]any {
	switch items := v.(type) {
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case []map[string]any:
		return items
	default:
		return nil
	}
}

func toStringSlice(v any) []string {
	switch val := v.(type) {
	case string:
		return []string{val}
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return val
	default:
		return nil
	}
}

// sanitizeConfigIDFields pre-processes raw config map items so that
// non-UUID `config_id` values are moved to `external_id` before JSON unmarshal.
func sanitizeConfigIDFields(configMap map[string]any) {
	for _, key := range []string{"access_logs", "logs", "config_access", "access"} {
		items := toMapSlice(configMap[key])
		for _, item := range items {
			v, ok := item["config_id"]
			if !ok {
				continue
			}
			s, ok := v.(string)
			if !ok {
				continue
			}
			if _, err := uuid.Parse(s); err != nil {
				if _, hasExt := item["external_id"]; !hasExt {
					item["external_id"] = s
				}
				delete(item, "config_id")
			}
		}
	}
}

func parseConfigRef(raw map[string]any) v1.ExternalID {
	var ext v1.ExternalID
	if v, ok := findStringKey(raw, "config_id", "uuid"); ok {
		if _, err := uuid.Parse(v); err == nil {
			ext.ConfigID = v
		} else {
			ext.ExternalID = v
		}
	}
	if v, ok := findStringKey(raw, "external_id"); ok {
		ext.ExternalID = v
	}
	if v, ok := findStringKey(raw, "config_type", "type"); ok {
		ext.ConfigType = v
	}
	return ext
}

func expandConfigAccessShorthand(configMap map[string]any, configAccess []v1.ExternalConfigAccess) {
	rawAccess, _ := findKey(configMap, "config_access", "access")
	rawItems := toMapSlice(rawAccess)

	for i := range configAccess {
		if i >= len(rawItems) {
			break
		}
		raw := rawItems[i]

		if len(configAccess[i].ExternalUserAliases) == 0 {
			if v, ok := raw["user"]; ok {
				configAccess[i].ExternalUserAliases = toStringSlice(v)
			}
		}
		if len(configAccess[i].ExternalRoleAliases) == 0 {
			if v, ok := raw["role"]; ok {
				configAccess[i].ExternalRoleAliases = toStringSlice(v)
			}
		}
		if len(configAccess[i].ExternalGroupAliases) == 0 {
			if v, ok := raw["group"]; ok {
				configAccess[i].ExternalGroupAliases = toStringSlice(v)
			}
		}
		if configAccess[i].ConfigExternalID.IsEmpty() && configAccess[i].ConfigID == uuid.Nil {
			ref := parseConfigRef(raw)
			if ref.ConfigID != "" {
				configAccess[i].ConfigID = uuid.MustParse(ref.ConfigID)
			} else {
				configAccess[i].ConfigExternalID = ref
			}
		}
	}
}

func expandAccessLogShorthand(configMap map[string]any, accessLogs []v1.ExternalConfigAccessLog) {
	rawLogs, _ := findKey(configMap, "access_logs", "logs")
	rawItems := toMapSlice(rawLogs)

	for i := range accessLogs {
		if i >= len(rawItems) {
			break
		}
		raw := rawItems[i]

		if len(accessLogs[i].ExternalUserAliases) == 0 {
			if v, ok := raw["user"]; ok {
				accessLogs[i].ExternalUserAliases = toStringSlice(v)
			}
		}
		if accessLogs[i].ConfigExternalID.IsEmpty() && accessLogs[i].ConfigID == uuid.Nil {
			ref := parseConfigRef(raw)
			if ref.ConfigID != "" {
				accessLogs[i].ConfigID = uuid.MustParse(ref.ConfigID)
			} else {
				accessLogs[i].ConfigExternalID = ref
			}
		}
	}
}

func applyConfigRefDefaults(configMap map[string]any, result *ExtractedConfig) {
	var defaultExternalID v1.ExternalID
	var defaultConfigID uuid.UUID

	if v, ok := findStringKey(configMap, "uuid", "config_id"); ok {
		if parsed, err := uuid.Parse(v); err == nil {
			defaultConfigID = parsed
		}
	}
	if v, ok := findStringKey(configMap, "external_id", "id"); ok {
		defaultExternalID.ExternalID = v
	}
	if v, ok := findStringKey(configMap, "type", "config_type"); ok {
		defaultExternalID.ConfigType = v
	}

	hasDefault := defaultConfigID != uuid.Nil || defaultExternalID.ExternalID != "" || defaultExternalID.ConfigType != ""
	if !hasDefault {
		return
	}

	for i := range result.ConfigAccess {
		if result.ConfigAccess[i].ConfigID != uuid.Nil || !result.ConfigAccess[i].ConfigExternalID.IsEmpty() {
			continue
		}
		if defaultConfigID != uuid.Nil {
			result.ConfigAccess[i].ConfigID = defaultConfigID
		} else {
			result.ConfigAccess[i].ConfigExternalID = defaultExternalID
		}
	}

	for i := range result.AccessLogs {
		if result.AccessLogs[i].ConfigID != uuid.Nil {
			continue
		}
		if defaultConfigID != uuid.Nil {
			result.AccessLogs[i].ConfigID = defaultConfigID
		} else if !defaultExternalID.IsEmpty() {
			result.AccessLogs[i].ConfigExternalID = defaultExternalID
		}
	}

	for i := range result.Changes {
		if result.Changes[i].ExternalID != "" && result.Changes[i].ConfigType != "" {
			continue
		}
		if result.Changes[i].ExternalID == "" {
			result.Changes[i].ExternalID = defaultExternalID.ExternalID
		}
		if result.Changes[i].ConfigType == "" {
			result.Changes[i].ConfigType = defaultExternalID.ConfigType
		}
	}

	for i := range result.Analysis {
		if result.Analysis[i].ExternalID != "" || len(result.Analysis[i].ExternalConfigs) > 0 {
			continue
		}
		result.Analysis[i].ExternalID = defaultExternalID.ExternalID
		result.Analysis[i].ConfigType = defaultExternalID.ConfigType
	}
}

func validateConfigRefs(result *ExtractedConfig) {
	var validChanges []v1.ChangeResult
	for _, c := range result.Changes {
		if c.ExternalID == "" {
			result.Summary.Changes.Skipped++
			result.AddWarning(v1.Warning{Error: "change missing external_id", Result: c})
			continue
		}
		if c.ExternalChangeID == "" {
			result.Summary.Changes.Skipped++
			result.AddWarning(v1.Warning{Error: "change missing external_change_id", Result: c})
			continue
		}
		if c.ChangeType == "" {
			result.Summary.Changes.Skipped++
			result.AddWarning(v1.Warning{Error: "change missing change_type", Result: c})
			continue
		}
		validChanges = append(validChanges, c)
	}
	result.Changes = validChanges

	var validAnalysis []v1.AnalysisResult
	for _, a := range result.Analysis {
		if a.ExternalID != "" || len(a.ExternalConfigs) > 0 {
			validAnalysis = append(validAnalysis, a)
			continue
		}
		result.Summary.Analysis.Skipped++
		result.AddWarning(v1.Warning{Error: "analysis missing external_id", Result: a})
	}
	result.Analysis = validAnalysis

	var validAccess []v1.ExternalConfigAccess
	for _, ca := range result.ConfigAccess {
		if ca.ConfigID == uuid.Nil && ca.ConfigExternalID.ExternalID == "" {
			result.Summary.ConfigAccess.Skipped++
			result.AddWarning(v1.Warning{Error: "config_access missing config reference", Result: ca})
			continue
		}
		if !ca.HasPrincipal() {
			result.Summary.ConfigAccess.Skipped++
			result.AddWarning(v1.Warning{Error: "config_access missing user/group reference", Result: ca})
			continue
		}
		validAccess = append(validAccess, ca)
	}
	result.ConfigAccess = validAccess

	var validLogs []v1.ExternalConfigAccessLog
	for _, al := range result.AccessLogs {
		if al.ConfigID == uuid.Nil && al.ConfigExternalID.ExternalID == "" {
			result.Summary.AccessLogs.Skipped++
			result.AddWarning(v1.Warning{Error: "access_log missing config reference", Result: al})
			continue
		}
		if al.ExternalUserID == uuid.Nil && len(al.ExternalUserAliases) == 0 {
			result.Summary.AccessLogs.Skipped++
			result.AddWarning(v1.Warning{Error: "access_log missing user reference", Result: al})
			continue
		}
		validLogs = append(validLogs, al)
	}
	result.AccessLogs = validLogs
}
