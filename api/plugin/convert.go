package plugin

import (
	"encoding/json"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	"github.com/samber/lo"

	v1 "github.com/flanksource/config-db/api"
	pb "github.com/flanksource/config-db/api/plugin/proto"
)

func ScrapeResultToProto(r v1.ScrapeResult) (*pb.ScrapeResultProto, error) {
	result := &pb.ScrapeResultProto{
		Id:           r.ID,
		Name:         r.Name,
		ConfigClass:  r.ConfigClass,
		ConfigType:   r.Type,
		Status:       r.Status,
		Health:       string(r.Health),
		Ready:        r.Ready,
		Description:  r.Description,
		Aliases:      r.Aliases,
		Source:       r.Source,
		Locations:    r.Locations,
		Format:       r.Format,
		Icon:         r.Icon,
		Labels:       r.Labels,
		Tags:         r.Tags,
		ScraperLess:  r.ScraperLess,
		DeleteReason: string(r.DeleteReason),
	}
	if r.ConfigID != nil {
		result.ConfigId = *r.ConfigID
	}
	if r.Error != nil {
		result.Error = r.Error.Error()
	}
	if r.CreatedAt != nil {
		result.CreatedAt = r.CreatedAt.UnixMilli()
	}
	if r.DeletedAt != nil {
		result.DeletedAt = r.DeletedAt.UnixMilli()
	}
	if r.Config != nil {
		configJSON, err := json.Marshal(r.Config)
		if err != nil {
			return nil, err
		}
		result.Config = configJSON
	}
	if r.AnalysisResult != nil {
		result.Analysis = analysisResultToProto(r.AnalysisResult)
	}
	for _, c := range r.Changes {
		result.Changes = append(result.Changes, changeResultToProto(c))
	}
	for _, rel := range r.RelationshipResults {
		result.Relationships = append(result.Relationships, relationshipResultToProto(rel))
	}
	for _, p := range r.Parents {
		result.Parents = append(result.Parents, configExternalKeyToProto(p))
	}
	for _, c := range r.Children {
		result.Children = append(result.Children, configExternalKeyToProto(c))
	}
	if len(r.Properties) > 0 {
		propsJSON, err := json.Marshal(r.Properties)
		if err != nil {
			return nil, err
		}
		result.Properties = propsJSON
	}
	for _, role := range r.ExternalRoles {
		result.ExternalRoles = append(result.ExternalRoles, externalRoleToProto(role))
	}
	for _, user := range r.ExternalUsers {
		result.ExternalUsers = append(result.ExternalUsers, externalUserToProto(user))
	}
	for _, group := range r.ExternalGroups {
		result.ExternalGroups = append(result.ExternalGroups, externalGroupToProto(group))
	}
	for _, ug := range r.ExternalUserGroups {
		result.ExternalUserGroups = append(result.ExternalUserGroups, externalUserGroupToProto(ug))
	}
	for _, ca := range r.ConfigAccess {
		result.ConfigAccess = append(result.ConfigAccess, externalConfigAccessToProto(ca))
	}
	for _, cal := range r.ConfigAccessLogs {
		result.ConfigAccessLogs = append(result.ConfigAccessLogs, externalConfigAccessLogToProto(cal))
	}
	baseJSON, err := json.Marshal(r.BaseScraper)
	if err != nil {
		return nil, err
	}
	result.BaseScraper = baseJSON
	return result, nil
}

func ProtoToScrapeResult(p *pb.ScrapeResultProto) (v1.ScrapeResult, error) {
	if p == nil {
		return v1.ScrapeResult{}, nil
	}
	result := v1.ScrapeResult{
		ID:          p.Id,
		Name:        p.Name,
		ConfigClass: p.ConfigClass,
		Type:        p.ConfigType,
		Status:      p.Status,
		Health:      models.Health(p.Health),
		Ready:       p.Ready,
		Description: p.Description,
		Aliases:     p.Aliases,
		Source:      p.Source,
		Locations:   p.Locations,
		Format:      p.Format,
		Icon:        p.Icon,
		Labels:      p.Labels,
		Tags:        p.Tags,
		ScraperLess: p.ScraperLess,
	}
	if p.ConfigId != "" {
		result.ConfigID = &p.ConfigId
	}
	if p.Error != "" {
		result.Error = &PluginError{Message: p.Error}
	}
	if p.CreatedAt > 0 {
		t := time.UnixMilli(p.CreatedAt)
		result.CreatedAt = &t
	}
	if p.DeletedAt > 0 {
		t := time.UnixMilli(p.DeletedAt)
		result.DeletedAt = &t
	}
	if p.DeleteReason != "" {
		result.DeleteReason = v1.ConfigDeleteReason(p.DeleteReason)
	}
	if len(p.Config) > 0 {
		var config any
		if err := json.Unmarshal(p.Config, &config); err != nil {
			return result, err
		}
		result.Config = config
	}
	if p.Analysis != nil {
		result.AnalysisResult = protoToAnalysisResult(p.Analysis)
	}
	for _, c := range p.Changes {
		result.Changes = append(result.Changes, protoToChangeResult(c))
	}
	for _, rel := range p.Relationships {
		result.RelationshipResults = append(result.RelationshipResults, protoToRelationshipResult(rel))
	}
	for _, parent := range p.Parents {
		result.Parents = append(result.Parents, protoToConfigExternalKey(parent))
	}
	for _, child := range p.Children {
		result.Children = append(result.Children, protoToConfigExternalKey(child))
	}
	if len(p.Properties) > 0 {
		var props types.Properties
		if err := json.Unmarshal(p.Properties, &props); err != nil {
			return result, err
		}
		result.Properties = props
	}
	for _, role := range p.ExternalRoles {
		result.ExternalRoles = append(result.ExternalRoles, protoToExternalRole(role))
	}
	for _, user := range p.ExternalUsers {
		result.ExternalUsers = append(result.ExternalUsers, protoToExternalUser(user))
	}
	for _, group := range p.ExternalGroups {
		result.ExternalGroups = append(result.ExternalGroups, protoToExternalGroup(group))
	}
	for _, ug := range p.ExternalUserGroups {
		result.ExternalUserGroups = append(result.ExternalUserGroups, protoToExternalUserGroup(ug))
	}
	for _, ca := range p.ConfigAccess {
		result.ConfigAccess = append(result.ConfigAccess, protoToExternalConfigAccess(ca))
	}
	for _, cal := range p.ConfigAccessLogs {
		result.ConfigAccessLogs = append(result.ConfigAccessLogs, protoToExternalConfigAccessLog(cal))
	}
	if len(p.BaseScraper) > 0 {
		if err := json.Unmarshal(p.BaseScraper, &result.BaseScraper); err != nil {
			return result, err
		}
	}
	return result, nil
}

func analysisResultToProto(a *v1.AnalysisResult) *pb.AnalysisResultProto {
	result := &pb.AnalysisResultProto{
		Summary:      a.Summary,
		AnalysisType: string(a.AnalysisType),
		Severity:     string(a.Severity),
		Source:       a.Source,
		Analyzer:     a.Analyzer,
		Messages:     a.Messages,
		Status:       a.Status,
	}
	if a.Analysis != nil {
		analysisJSON, _ := json.Marshal(a.Analysis)
		result.Analysis = analysisJSON
	}
	if a.FirstObserved != nil {
		result.FirstObserved = a.FirstObserved.UnixMilli()
	}
	if a.LastObserved != nil {
		result.LastObserved = a.LastObserved.UnixMilli()
	}
	for _, ec := range a.ExternalConfigs {
		result.ExternalConfigs = append(result.ExternalConfigs, &pb.ExternalIDProto{
			ExternalId: ec.ExternalID,
			ConfigType: ec.ConfigType,
			ScraperId:  ec.ScraperID,
		})
	}
	return result
}

func protoToAnalysisResult(p *pb.AnalysisResultProto) *v1.AnalysisResult {
	result := &v1.AnalysisResult{
		Summary:      p.Summary,
		AnalysisType: models.AnalysisType(p.AnalysisType),
		Severity:     models.Severity(p.Severity),
		Source:       p.Source,
		Analyzer:     p.Analyzer,
		Messages:     p.Messages,
		Status:       p.Status,
	}
	if len(p.Analysis) > 0 {
		var analysis map[string]any
		if err := json.Unmarshal(p.Analysis, &analysis); err != nil {
			logger.Warnf("failed to unmarshal analysis: %v", err)
		}
		result.Analysis = analysis
	}
	if p.FirstObserved > 0 {
		t := time.UnixMilli(p.FirstObserved)
		result.FirstObserved = &t
	}
	if p.LastObserved > 0 {
		t := time.UnixMilli(p.LastObserved)
		result.LastObserved = &t
	}
	for _, ec := range p.ExternalConfigs {
		result.ExternalConfigs = append(result.ExternalConfigs, v1.ExternalID{
			ExternalID: ec.ExternalId,
			ConfigType: ec.ConfigType,
			ScraperID:  ec.ScraperId,
		})
	}
	return result
}

func changeResultToProto(c v1.ChangeResult) *pb.ChangeResultProto {
	result := &pb.ChangeResultProto{
		ExternalId:       c.ExternalID,
		ConfigType:       c.ConfigType,
		ScraperId:        c.ScraperID,
		ExternalChangeId: c.ExternalChangeID,
		Action:           string(c.Action),
		ChangeType:       c.ChangeType,
		Patches:          c.Patches,
		Summary:          c.Summary,
		Severity:         c.Severity,
		Source:           c.Source,
		ConfigId:         c.ConfigID,
		UpdateExisting:   c.UpdateExisting,
	}
	if c.CreatedBy != nil {
		result.CreatedBy = *c.CreatedBy
	}
	if c.CreatedAt != nil {
		result.CreatedAt = c.CreatedAt.UnixMilli()
	}
	if c.Diff != nil {
		result.Diff = *c.Diff
	}
	if c.Details != nil {
		detailsJSON, _ := json.Marshal(c.Details)
		result.Details = detailsJSON
	}
	return result
}

func protoToChangeResult(p *pb.ChangeResultProto) v1.ChangeResult {
	result := v1.ChangeResult{
		ExternalID:       p.ExternalId,
		ConfigType:       p.ConfigType,
		ScraperID:        p.ScraperId,
		ExternalChangeID: p.ExternalChangeId,
		Action:           v1.ChangeAction(p.Action),
		ChangeType:       p.ChangeType,
		Patches:          p.Patches,
		Summary:          p.Summary,
		Severity:         p.Severity,
		Source:           p.Source,
		ConfigID:         p.ConfigId,
		UpdateExisting:   p.UpdateExisting,
	}
	if p.CreatedBy != "" {
		result.CreatedBy = &p.CreatedBy
	}
	if p.CreatedAt > 0 {
		t := time.UnixMilli(p.CreatedAt)
		result.CreatedAt = &t
	}
	if p.Diff != "" {
		result.Diff = &p.Diff
	}
	if len(p.Details) > 0 {
		var details map[string]any
		if err := json.Unmarshal(p.Details, &details); err != nil {
			logger.Warnf("failed to unmarshal details: %v", err)
		}
		result.Details = details
	}
	return result
}

func relationshipResultToProto(r v1.RelationshipResult) *pb.RelationshipResultProto {
	return &pb.RelationshipResultProto{
		ConfigId: r.ConfigID,
		ConfigExternalId: &pb.ExternalIDProto{
			ExternalId: r.ConfigExternalID.ExternalID,
			ConfigType: r.ConfigExternalID.ConfigType,
			ScraperId:  r.ConfigExternalID.ScraperID,
		},
		RelatedExternalId: &pb.ExternalIDProto{
			ExternalId: r.RelatedExternalID.ExternalID,
			ConfigType: r.RelatedExternalID.ConfigType,
			ScraperId:  r.RelatedExternalID.ScraperID,
		},
		RelatedConfigId: r.RelatedConfigID,
		Relationship:    r.Relationship,
	}
}

func protoToRelationshipResult(p *pb.RelationshipResultProto) v1.RelationshipResult {
	result := v1.RelationshipResult{
		ConfigID:        p.ConfigId,
		RelatedConfigID: p.RelatedConfigId,
		Relationship:    p.Relationship,
	}
	if p.ConfigExternalId != nil {
		result.ConfigExternalID = v1.ExternalID{
			ExternalID: p.ConfigExternalId.ExternalId,
			ConfigType: p.ConfigExternalId.ConfigType,
			ScraperID:  p.ConfigExternalId.ScraperId,
		}
	}
	if p.RelatedExternalId != nil {
		result.RelatedExternalID = v1.ExternalID{
			ExternalID: p.RelatedExternalId.ExternalId,
			ConfigType: p.RelatedExternalId.ConfigType,
			ScraperID:  p.RelatedExternalId.ScraperId,
		}
	}
	return result
}

func configExternalKeyToProto(c v1.ConfigExternalKey) *pb.ConfigExternalKeyProto {
	return &pb.ConfigExternalKeyProto{
		ExternalId: c.ExternalID,
		Type:       c.Type,
		ScraperId:  c.ScraperID,
	}
}

func protoToConfigExternalKey(p *pb.ConfigExternalKeyProto) v1.ConfigExternalKey {
	return v1.ConfigExternalKey{
		ExternalID: p.ExternalId,
		Type:       p.Type,
		ScraperID:  p.ScraperId,
	}
}

func externalRoleToProto(r models.ExternalRole) *pb.ExternalRoleProto {
	return &pb.ExternalRoleProto{
		Id:          r.ID.String(),
		AccountId:   r.AccountID,
		ScraperId:   lo.FromPtr(r.ScraperID).String(),
		Name:        r.Name,
		Description: r.Description,
	}
}

func protoToExternalRole(p *pb.ExternalRoleProto) models.ExternalRole {
	role := models.ExternalRole{
		Name:        p.Name,
		AccountID:   p.AccountId,
		Description: p.Description,
	}
	if p.Id != "" {
		role.ID = uuid.MustParse(p.Id)
	}
	if p.ScraperId != "" {
		id := uuid.MustParse(p.ScraperId)
		role.ScraperID = &id
	}
	return role
}

func externalUserToProto(u models.ExternalUser) *pb.ExternalUserProto {
	result := &pb.ExternalUserProto{
		Id:        u.ID.String(),
		Name:      u.Name,
		ScraperId: u.ScraperID.String(),
		AccountId: u.AccountID,
		UserType:  u.UserType,
	}
	if u.Email != nil {
		result.Email = *u.Email
	}
	result.CreatedAt = u.CreatedAt.UnixMilli()
	if u.DeletedAt != nil {
		result.DeletedAt = u.DeletedAt.UnixMilli()
	}
	return result
}

func protoToExternalUser(p *pb.ExternalUserProto) models.ExternalUser {
	user := models.ExternalUser{
		Name:      p.Name,
		AccountID: p.AccountId,
		UserType:  p.UserType,
	}
	if p.Id != "" {
		user.ID = uuid.MustParse(p.Id)
	}
	if p.ScraperId != "" {
		user.ScraperID = uuid.MustParse(p.ScraperId)
	}
	if p.Email != "" {
		user.Email = &p.Email
	}
	if p.CreatedAt > 0 {
		user.CreatedAt = time.UnixMilli(p.CreatedAt)
	}
	if p.DeletedAt > 0 {
		t := time.UnixMilli(p.DeletedAt)
		user.DeletedAt = &t
	}
	return user
}

func externalGroupToProto(g models.ExternalGroup) *pb.ExternalGroupProto {
	result := &pb.ExternalGroupProto{
		Id:        g.ID.String(),
		AccountId: g.AccountID,
		ScraperId: g.ScraperID.String(),
		Name:      g.Name,
		GroupType: g.GroupType,
		CreatedAt: g.CreatedAt.UnixMilli(),
	}
	if g.DeletedAt != nil {
		result.DeletedAt = g.DeletedAt.UnixMilli()
	}
	return result
}

func protoToExternalGroup(p *pb.ExternalGroupProto) models.ExternalGroup {
	group := models.ExternalGroup{
		Name:      p.Name,
		AccountID: p.AccountId,
		GroupType: p.GroupType,
	}
	if p.Id != "" {
		group.ID = uuid.MustParse(p.Id)
	}
	if p.ScraperId != "" {
		group.ScraperID = uuid.MustParse(p.ScraperId)
	}
	if p.CreatedAt > 0 {
		group.CreatedAt = time.UnixMilli(p.CreatedAt)
	}
	if p.DeletedAt > 0 {
		t := time.UnixMilli(p.DeletedAt)
		group.DeletedAt = &t
	}
	return group
}

func externalUserGroupToProto(ug models.ExternalUserGroup) *pb.ExternalUserGroupProto {
	return &pb.ExternalUserGroupProto{
		ExternalUserId:  ug.ExternalUserID.String(),
		ExternalGroupId: ug.ExternalGroupID.String(),
	}
}

func protoToExternalUserGroup(p *pb.ExternalUserGroupProto) models.ExternalUserGroup {
	return models.ExternalUserGroup{
		ExternalUserID:  uuid.MustParse(p.ExternalUserId),
		ExternalGroupID: uuid.MustParse(p.ExternalGroupId),
	}
}

func externalConfigAccessToProto(ca v1.ExternalConfigAccess) *pb.ExternalConfigAccessProto {
	result := &pb.ExternalConfigAccessProto{
		Id:                   ca.ID,
		ConfigId:             ca.ConfigID.String(),
		ScraperId:            lo.FromPtr(ca.ScraperID).String(),
		ExternalUserAliases:  ca.ExternalUserAliases,
		ExternalRoleAliases:  ca.ExternalRoleAliases,
		ExternalGroupAliases: ca.ExternalGroupAliases,
		ConfigExternalId: &pb.ExternalIDProto{
			ExternalId: ca.ConfigExternalID.ExternalID,
			ConfigType: ca.ConfigExternalID.ConfigType,
			ScraperId:  ca.ConfigExternalID.ScraperID,
		},
	}
	if ca.ExternalUserID != nil {
		result.ExternalUserId = ca.ExternalUserID.String()
	}
	if ca.ExternalRoleID != nil {
		result.ExternalRoleId = ca.ExternalRoleID.String()
	}
	if ca.ExternalGroupID != nil {
		result.ExternalGroupId = ca.ExternalGroupID.String()
	}
	result.CreatedAt = ca.CreatedAt.UnixMilli()
	if ca.DeletedAt != nil {
		result.DeletedAt = ca.DeletedAt.UnixMilli()
	}
	return result
}

func protoToExternalConfigAccess(p *pb.ExternalConfigAccessProto) v1.ExternalConfigAccess {
	ca := v1.ExternalConfigAccess{
		ExternalUserAliases:  p.ExternalUserAliases,
		ExternalRoleAliases:  p.ExternalRoleAliases,
		ExternalGroupAliases: p.ExternalGroupAliases,
	}
	ca.ID = p.Id
	if p.ConfigId != "" {
		ca.ConfigID = uuid.MustParse(p.ConfigId)
	}
	if p.ScraperId != "" {
		id := uuid.MustParse(p.ScraperId)
		ca.ScraperID = &id
	}
	if p.ExternalUserId != "" {
		id := uuid.MustParse(p.ExternalUserId)
		ca.ExternalUserID = &id
	}
	if p.ExternalRoleId != "" {
		id := uuid.MustParse(p.ExternalRoleId)
		ca.ExternalRoleID = &id
	}
	if p.ExternalGroupId != "" {
		id := uuid.MustParse(p.ExternalGroupId)
		ca.ExternalGroupID = &id
	}
	if p.CreatedAt > 0 {
		ca.CreatedAt = time.UnixMilli(p.CreatedAt)
	}
	if p.DeletedAt > 0 {
		t := time.UnixMilli(p.DeletedAt)
		ca.DeletedAt = &t
	}
	if p.ConfigExternalId != nil {
		ca.ConfigExternalID = v1.ExternalID{
			ExternalID: p.ConfigExternalId.ExternalId,
			ConfigType: p.ConfigExternalId.ConfigType,
			ScraperID:  p.ConfigExternalId.ScraperId,
		}
	}
	return ca
}

func externalConfigAccessLogToProto(cal v1.ExternalConfigAccessLog) *pb.ExternalConfigAccessLogProto {
	result := &pb.ExternalConfigAccessLogProto{
		ConfigId:       cal.ConfigID.String(),
		ExternalUserId: cal.ExternalUserID.String(),
		CreatedAt:      cal.CreatedAt.UnixMilli(),
		ConfigExternalId: &pb.ExternalIDProto{
			ExternalId: cal.ConfigExternalID.ExternalID,
			ConfigType: cal.ConfigExternalID.ConfigType,
			ScraperId:  cal.ConfigExternalID.ScraperID,
		},
	}
	return result
}

func protoToExternalConfigAccessLog(p *pb.ExternalConfigAccessLogProto) v1.ExternalConfigAccessLog {
	cal := v1.ExternalConfigAccessLog{}
	if p.ConfigId != "" {
		cal.ConfigID = uuid.MustParse(p.ConfigId)
	}
	if p.ExternalUserId != "" {
		cal.ExternalUserID = uuid.MustParse(p.ExternalUserId)
	}
	if p.CreatedAt > 0 {
		cal.CreatedAt = time.UnixMilli(p.CreatedAt)
	}
	if p.ConfigExternalId != nil {
		cal.ConfigExternalID = v1.ExternalID{
			ExternalID: p.ConfigExternalId.ExternalId,
			ConfigType: p.ConfigExternalId.ConfigType,
			ScraperID:  p.ConfigExternalId.ScraperId,
		}
	}
	return cal
}
