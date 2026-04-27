package scrapers

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/commons/hash"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
	dutymodels "github.com/flanksource/duty/models"
	dutytypes "github.com/flanksource/duty/types"
	"github.com/flanksource/gomplate/v3"
	"github.com/google/uuid"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"gopkg.in/yaml.v3"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

type e2ePrePopulateConfig struct {
	ConfigClass     string            `yaml:"config_class"`
	ID              string            `yaml:"id,omitempty"`
	DeterministicID bool              `yaml:"deterministic_id,omitempty"`
	Type            string            `yaml:"type"`
	Name            string            `yaml:"name"`
	ExternalID      []string          `yaml:"external_id"`
	Config          string            `yaml:"config"`
	Properties      map[string]string `yaml:"properties,omitempty"`
}

type e2ePrePopulateExternalUser struct {
	ID        string   `yaml:"id,omitempty"`
	Name      string   `yaml:"name"`
	Aliases   []string `yaml:"aliases"`
	Email     string   `yaml:"email,omitempty"`
	UserType  string   `yaml:"user_type,omitempty"`
	AccountID string   `yaml:"account_id,omitempty"`
}

type e2ePrePopulateExternalGroup struct {
	ID        string   `yaml:"id,omitempty"`
	Name      string   `yaml:"name"`
	Aliases   []string `yaml:"aliases"`
	GroupType string   `yaml:"group_type,omitempty"`
	AccountID string   `yaml:"account_id,omitempty"`
}

type e2ePrePopulateExternalRole struct {
	Name        string   `yaml:"name"`
	Aliases     []string `yaml:"aliases"`
	RoleType    string   `yaml:"role_type,omitempty"`
	AccountID   string   `yaml:"account_id,omitempty"`
	Description string   `yaml:"description,omitempty"`
}

type e2ePrePopulateConfigAccess struct {
	ID                   string   `yaml:"id"`
	ConfigExternalID     string   `yaml:"config_external_id"`
	ExternalUserAliases  []string `yaml:"external_user_aliases,omitempty"`
	ExternalGroupAliases []string `yaml:"external_group_aliases,omitempty"`
	ExternalRoleAliases  []string `yaml:"external_role_aliases,omitempty"`
	Source               string   `yaml:"source,omitempty"`
	CreatedAtOffsetMins  int      `yaml:"created_at_offset_mins,omitempty"`
}

type e2ePrePopulateConfigAccessLog struct {
	ConfigExternalID    string         `yaml:"config_external_id"`
	ExternalUserAliases []string       `yaml:"external_user_aliases"`
	Count               int            `yaml:"count,omitempty"`
	MFA                 *bool          `yaml:"mfa,omitempty"`
	Properties          map[string]any `yaml:"properties,omitempty"`
	CreatedAtOffsetMins int            `yaml:"created_at_offset_mins,omitempty"`
}

type e2ePrePopulateExternalUserGroup struct {
	ExternalUserAliases  []string `yaml:"external_user_aliases"`
	ExternalGroupAliases []string `yaml:"external_group_aliases"`
	Owner                string   `yaml:"owner,omitempty"`
	Deleted              bool     `yaml:"deleted,omitempty"`
}

type e2ePrePopulateConfigRelationship struct {
	ConfigExternalID  string `yaml:"config_external_id"`
	RelatedExternalID string `yaml:"related_external_id"`
	Relation          string `yaml:"relation,omitempty"`
	Owner             string `yaml:"owner,omitempty"`
	Deleted           bool   `yaml:"deleted,omitempty"`
}

type e2ePrePopulate struct {
	Configs             []e2ePrePopulateConfig             `yaml:"configs"`
	ExternalUsers       []e2ePrePopulateExternalUser       `yaml:"external_users"`
	ExternalGroups      []e2ePrePopulateExternalGroup      `yaml:"external_groups"`
	ExternalRoles       []e2ePrePopulateExternalRole       `yaml:"external_roles"`
	ExternalUserGroups  []e2ePrePopulateExternalUserGroup  `yaml:"external_user_groups"`
	ConfigRelationships []e2ePrePopulateConfigRelationship `yaml:"config_relationships"`
	ConfigAccess        []e2ePrePopulateConfigAccess       `yaml:"config_access"`
	ConfigAccessLogs    []e2ePrePopulateConfigAccessLog    `yaml:"config_access_logs"`
}

type e2eFixture struct {
	Spec        map[string]any `yaml:"spec"`
	PrePopulate e2ePrePopulate `yaml:"pre_populate"`
	Assertions  []string       `yaml:"assertions"`
	Incremental bool           `yaml:"incremental,omitempty"`
}

var _ = Describe("e2e extraction fixtures", func() {
	fixtures, err := filepath.Glob("extract/testdata/e2e/*.yaml")
	if err != nil {
		panic(err)
	}

	for _, fixturePath := range fixtures {
		name := filepath.Base(fixturePath)

		It("e2e fixture: "+name, func() {
			data, err := os.ReadFile("scrapers/" + fixturePath)
			Expect(err).ToNot(HaveOccurred())

			var fixture e2eFixture
			Expect(yaml.Unmarshal(data, &fixture)).To(Succeed())
			Expect(fixture.Spec).ToNot(BeNil(), "e2e fixture %s must have a spec field", name)
			Expect(fixture.Assertions).ToNot(BeEmpty(), "fixture %s has no assertions", name)

			specJSON, err := json.Marshal(fixture.Spec)
			Expect(err).ToNot(HaveOccurred())
			decoder := json.NewDecoder(bytes.NewReader(specJSON))
			decoder.DisallowUnknownFields()
			var specValidation v1.ScraperSpec
			Expect(decoder.Decode(&specValidation)).To(Succeed(), "spec in %s contains unknown fields", name)

			scrapeConfigYAML := buildScrapeConfigYAML(name, fixture.Spec)
			tmpFile, err := os.CreateTemp("", "e2e-fixture-*.yaml")
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = os.Remove(tmpFile.Name()) }()
			_, err = tmpFile.Write(scrapeConfigYAML)
			Expect(err).ToNot(HaveOccurred())
			Expect(tmpFile.Close()).To(Succeed())

			configs, err := v1.ParseConfigs(tmpFile.Name())
			Expect(err).ToNot(HaveOccurred())
			Expect(configs).ToNot(BeEmpty())
			config := configs[0]

			var createdItems []string
			configIDByExternalID := make(map[string]uuid.UUID)
			scraperModel, err := db.PersistScrapeConfigFromFile(DefaultContext, config)
			Expect(err).ToNot(HaveOccurred())
			config.SetUID(k8sTypes.UID(scraperModel.ID.String()))
			otherScraperModel := dutymodels.ConfigScraper{
				ID:        uuid.New(),
				Name:      "e2e-other-" + name,
				Namespace: "default",
				Spec:      "{}",
				Source:    dutymodels.SourceConfigFile,
			}
			otherScraperCreated := false
			ownerScraperID := func(owner string) uuid.UUID {
				if owner == "other" {
					if !otherScraperCreated {
						Expect(DefaultContext.DB().Create(&otherScraperModel).Error).ToNot(HaveOccurred())
						otherScraperCreated = true
					}
					return otherScraperModel.ID
				}
				return scraperModel.ID
			}

			for _, preConfig := range fixture.PrePopulate.Configs {
				configID := preConfig.ID
				if configID == "" && preConfig.DeterministicID {
					Expect(preConfig.ExternalID).ToNot(BeEmpty(), "deterministic_id requires external_id")
					id, err := hash.DeterministicUUID(preConfig.ExternalID[0])
					Expect(err).ToNot(HaveOccurred())
					configID = id.String()
				}
				if configID == "" {
					configID = uuid.NewString()
				}
				ci := &models.ConfigItem{
					ID:          configID,
					ConfigClass: preConfig.ConfigClass,
					Type:        preConfig.Type,
					Name:        lo.ToPtr(preConfig.Name),
					ExternalID:  preConfig.ExternalID,
					ScraperID:   &scraperModel.ID,
					Config:      lo.ToPtr(preConfig.Config),
				}
				if len(preConfig.Properties) > 0 {
					props := make(dutytypes.Properties, 0, len(preConfig.Properties))
					for k, v := range preConfig.Properties {
						props = append(props, &dutytypes.Property{Name: k, Text: v})
					}
					ci.Properties = &props
				}
				Expect(DefaultContext.DB().Create(ci).Error).ToNot(HaveOccurred())
				createdItems = append(createdItems, ci.ID)
				for _, externalID := range preConfig.ExternalID {
					configIDByExternalID[externalID] = uuid.MustParse(ci.ID)
				}
			}

			// Pre-populate external entities
			now := time.Now()
			userIDByAlias := make(map[string]uuid.UUID)
			groupIDByAlias := make(map[string]uuid.UUID)
			roleIDByAlias := make(map[string]uuid.UUID)
			for _, u := range fixture.PrePopulate.ExternalUsers {
				id := uuid.New()
				if u.ID != "" {
					id = uuid.MustParse(u.ID)
				}
				eu := dutymodels.ExternalUser{
					ID:        id,
					Name:      u.Name,
					Aliases:   pq.StringArray(u.Aliases),
					UserType:  u.UserType,
					Tenant:    u.AccountID,
					Email:     lo.Ternary(u.Email != "", &u.Email, nil),
					ScraperID: scraperModel.ID,
					CreatedAt: now,
					UpdatedAt: &now,
				}
				Expect(DefaultContext.DB().Create(&eu).Error).ToNot(HaveOccurred())
				for _, alias := range u.Aliases {
					userIDByAlias[alias] = eu.ID
				}
			}
			for _, g := range fixture.PrePopulate.ExternalGroups {
				id := uuid.New()
				if g.ID != "" {
					id = uuid.MustParse(g.ID)
				}
				eg := dutymodels.ExternalGroup{
					ID:        id,
					Name:      g.Name,
					Aliases:   pq.StringArray(g.Aliases),
					GroupType: g.GroupType,
					Tenant:    g.AccountID,
					ScraperID: scraperModel.ID,
					CreatedAt: now,
					UpdatedAt: &now,
				}
				Expect(DefaultContext.DB().Create(&eg).Error).ToNot(HaveOccurred())
				for _, alias := range g.Aliases {
					groupIDByAlias[alias] = eg.ID
				}
			}
			for _, r := range fixture.PrePopulate.ExternalRoles {
				er := dutymodels.ExternalRole{
					ID:          uuid.New(),
					Name:        r.Name,
					Aliases:     pq.StringArray(r.Aliases),
					RoleType:    r.RoleType,
					Tenant:      r.AccountID,
					Description: r.Description,
					ScraperID:   &scraperModel.ID,
					CreatedAt:   now,
					UpdatedAt:   &now,
				}
				Expect(DefaultContext.DB().Create(&er).Error).ToNot(HaveOccurred())
				for _, alias := range r.Aliases {
					roleIDByAlias[alias] = er.ID
				}
			}

			for _, ug := range fixture.PrePopulate.ExternalUserGroups {
				Expect(ug.ExternalUserAliases).ToNot(BeEmpty(), "external_user_groups fixture row must have external_user_aliases")
				Expect(ug.ExternalGroupAliases).ToNot(BeEmpty(), "external_user_groups fixture row must have external_group_aliases")
				userID, ok := userIDByAlias[ug.ExternalUserAliases[0]]
				Expect(ok).To(BeTrue(), "missing pre-populated external user alias %s", ug.ExternalUserAliases[0])
				groupID, ok := groupIDByAlias[ug.ExternalGroupAliases[0]]
				Expect(ok).To(BeTrue(), "missing pre-populated external group alias %s", ug.ExternalGroupAliases[0])
				row := dutymodels.ExternalUserGroup{
					ExternalUserID:  userID,
					ExternalGroupID: groupID,
					ScraperID:       ownerScraperID(ug.Owner),
					CreatedAt:       now,
				}
				if ug.Deleted {
					deletedAt := now
					row.DeletedAt = &deletedAt
				}
				Expect(DefaultContext.DB().Create(&row).Error).ToNot(HaveOccurred())
			}

			for _, rel := range fixture.PrePopulate.ConfigRelationships {
				configID, ok := configIDByExternalID[rel.ConfigExternalID]
				Expect(ok).To(BeTrue(), "missing pre-populated config external id %s", rel.ConfigExternalID)
				relatedID, ok := configIDByExternalID[rel.RelatedExternalID]
				Expect(ok).To(BeTrue(), "missing pre-populated related external id %s", rel.RelatedExternalID)
				row := models.ConfigRelationship{
					ConfigID:  configID.String(),
					RelatedID: relatedID.String(),
					Relation:  rel.Relation,
					ScraperID: ownerScraperID(rel.Owner),
				}
				if rel.Deleted {
					deletedAt := now
					Expect(DefaultContext.DB().Create(&row).Error).ToNot(HaveOccurred())
					Expect(DefaultContext.DB().Model(&models.ConfigRelationship{}).
						Where("config_id = ? AND related_id = ? AND relation = ? AND scraper_id = ?", row.ConfigID, row.RelatedID, row.Relation, row.ScraperID).
						Update("deleted_at", deletedAt).Error).ToNot(HaveOccurred())
				} else {
					Expect(DefaultContext.DB().Create(&row).Error).ToNot(HaveOccurred())
				}
			}

			for _, ca := range fixture.PrePopulate.ConfigAccess {
				row := dutymodels.ConfigAccess{
					ID:        ca.ID,
					ConfigID:  configIDByExternalID[ca.ConfigExternalID],
					ScraperID: &scraperModel.ID,
					CreatedAt: now.Add(time.Duration(ca.CreatedAtOffsetMins) * time.Minute),
				}
				if len(ca.ExternalUserAliases) > 0 {
					id, ok := userIDByAlias[ca.ExternalUserAliases[0]]
					Expect(ok).To(BeTrue(), "missing pre-populated external user alias %s", ca.ExternalUserAliases[0])
					row.ExternalUserID = &id
				}
				if len(ca.ExternalGroupAliases) > 0 {
					id, ok := groupIDByAlias[ca.ExternalGroupAliases[0]]
					Expect(ok).To(BeTrue(), "missing pre-populated external group alias %s", ca.ExternalGroupAliases[0])
					row.ExternalGroupID = &id
				}
				if len(ca.ExternalRoleAliases) > 0 {
					id, ok := roleIDByAlias[ca.ExternalRoleAliases[0]]
					Expect(ok).To(BeTrue(), "missing pre-populated external role alias %s", ca.ExternalRoleAliases[0])
					row.ExternalRoleID = &id
				}
				if ca.Source != "" {
					row.Source = lo.ToPtr(ca.Source)
				}
				Expect(DefaultContext.DB().Create(&row).Error).ToNot(HaveOccurred())
			}

			for _, log := range fixture.PrePopulate.ConfigAccessLogs {
				id, ok := userIDByAlias[log.ExternalUserAliases[0]]
				Expect(ok).To(BeTrue(), "missing pre-populated external user alias %s", log.ExternalUserAliases[0])
				count := log.Count
				row := dutymodels.ConfigAccessLog{
					ConfigID:       configIDByExternalID[log.ConfigExternalID],
					ExternalUserID: id,
					ScraperID:      scraperModel.ID,
					CreatedAt:      now.Add(time.Duration(log.CreatedAtOffsetMins) * time.Minute),
					Count:          &count,
					Properties:     log.Properties,
				}
				if log.MFA != nil {
					row.MFA = *log.MFA
				}
				Expect(DefaultContext.DB().Create(&row).Error).ToNot(HaveOccurred())
			}

			defer func() {
				DefaultContext.DB().Exec("DELETE FROM config_relationships WHERE config_id IN (SELECT id FROM config_items WHERE scraper_id = ?) OR related_id IN (SELECT id FROM config_items WHERE scraper_id = ?)", scraperModel.ID, scraperModel.ID)
				DefaultContext.DB().Exec("DELETE FROM config_access_logs WHERE scraper_id = ?", scraperModel.ID)
				DefaultContext.DB().Exec("DELETE FROM config_access WHERE scraper_id = ?", scraperModel.ID)
				for _, id := range createdItems {
					DefaultContext.DB().Where("config_id = ?", id).Delete(&models.ConfigChange{})
					DefaultContext.DB().Delete(&models.ConfigItem{}, "id = ?", id)
				}
				// Clean up external entities for this scraper
				DefaultContext.DB().Exec("DELETE FROM external_user_groups WHERE scraper_id IN ?", []uuid.UUID{scraperModel.ID, otherScraperModel.ID})
				DefaultContext.DB().Exec("DELETE FROM external_users WHERE scraper_id = ?", scraperModel.ID)
				DefaultContext.DB().Exec("DELETE FROM external_groups WHERE scraper_id = ?", scraperModel.ID)
				DefaultContext.DB().Exec("DELETE FROM external_roles WHERE scraper_id = ?", scraperModel.ID)
				DefaultContext.DB().Exec("DELETE FROM config_changes WHERE config_id IN (SELECT id FROM config_items WHERE scraper_id = ?)", scraperModel.ID)
				DefaultContext.DB().Exec("DELETE FROM config_items WHERE scraper_id = ?", scraperModel.ID)
				DefaultContext.DB().Where("id = ?", scraperModel.ID).Delete(&dutymodels.ConfigScraper{})
				if otherScraperCreated {
					DefaultContext.DB().Where("id = ?", otherScraperModel.ID).Delete(&dutymodels.ConfigScraper{})
				}
			}()

			scraperCtx := ctx.WithScrapeConfig(&config)
			scraperCtx, err = scraperCtx.InitTempCache()
			Expect(err).ToNot(HaveOccurred())
			if fixture.Incremental {
				scraperCtx = scraperCtx.AsIncrementalScrape()
			}

			results, err := Run(scraperCtx)
			Expect(err).ToNot(HaveOccurred())

			summary, err := db.SaveResults(scraperCtx, results)
			Expect(err).ToNot(HaveOccurred())

			env := buildE2EEnv(results, summary)
			env["scraper_id"] = scraperModel.ID.String()
			env["other_scraper_id"] = otherScraperModel.ID.String()

			var configsJSON string
			Expect(DefaultContext.DB().Raw(`
				SELECT COALESCE(jsonb_agg(jsonb_build_object(
					'id', id::text,
					'type', type,
					'name', name,
					'external_id', external_id,
					'scraper_id', scraper_id::text,
					'properties', COALESCE(to_jsonb(properties), '[]'::jsonb)
				) ORDER BY type, name), '[]'::jsonb)::text
				FROM config_items
				WHERE scraper_id = ?
			`, scraperModel.ID).Scan(&configsJSON).Error).ToNot(HaveOccurred())
			var configsSlice []any
			_ = json.Unmarshal([]byte(configsJSON), &configsSlice)
			if configsSlice == nil {
				configsSlice = []any{}
			}
			env["configs"] = configsSlice

			var rels []map[string]any
			DefaultContext.DB().Raw(`
				SELECT DISTINCT config_id::text, related_id::text, relation
				FROM config_relationships
				WHERE deleted_at IS NULL
					AND (
						config_id IN (SELECT id FROM config_items WHERE scraper_id = ?)
						OR related_id IN (SELECT id FROM config_items WHERE scraper_id = ?)
					)
				ORDER BY config_id::text, related_id::text, relation
			`, scraperModel.ID, scraperModel.ID).Scan(&rels)
			relsJSON, _ := json.Marshal(rels)
			var relsSlice []any
			_ = json.Unmarshal(relsJSON, &relsSlice)
			if relsSlice == nil {
				relsSlice = []any{}
			}
			env["relationships"] = relsSlice

			for _, expr := range fixture.Assertions {
				ok, err := DefaultContext.RunTemplateBool(gomplate.Template{Expression: expr}, env)
				Expect(err).ToNot(HaveOccurred(), "CEL error in %s: %s", name, expr)
				Expect(ok).To(BeTrue(), "assertion failed in %s: %s\nenv: %v", name, expr, env)
			}
		})
	}
})

func buildScrapeConfigYAML(name string, spec map[string]any) []byte {
	doc := map[string]any{
		"apiVersion": "configs.flanksource.com/v1",
		"kind":       "ScrapeConfig",
		"metadata": map[string]any{
			"name": "e2e-" + name,
		},
		"spec": spec,
	}
	out, err := yaml.Marshal(doc)
	Expect(err).ToNot(HaveOccurred())
	return out
}

func buildE2EEnv(results []v1.ScrapeResult, summary v1.ScrapeSummary) map[string]any {
	var allChanges []v1.ChangeResult
	for _, r := range results {
		allChanges = append(allChanges, r.Changes...)
	}

	env := map[string]any{
		"config": nil,
	}

	changesRaw, _ := json.Marshal(allChanges)
	var changesSlice []any
	_ = json.Unmarshal(changesRaw, &changesSlice)
	if changesSlice == nil {
		changesSlice = []any{}
	}
	env["changes"] = changesSlice

	for _, key := range []string{"analysis", "access_logs", "config_access", "external_users", "external_groups", "external_user_groups", "external_roles", "warnings"} {
		env[key] = []any{}
	}

	totals := summary.Totals()
	env["summary"] = map[string]any{
		"changes": map[string]any{
			"scraped": len(allChanges),
			"saved":   totals.Changes,
		},
	}

	return env
}
