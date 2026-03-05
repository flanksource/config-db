package extract

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/google/uuid"
)

// ExtractFullMode processes scraped results in full mode, extracting changes, access logs,
// config access, external users, groups, user groups, and roles from the config.
func ExtractFullMode(resolver Resolver, scraperID *uuid.UUID, originalConfig any, scraped v1.ScrapeResults) v1.ScrapeResults {
	all := ExtractedConfig{}
	for i := range scraped {
		extracted, err := ExtractConfigChangesFromConfig(resolver, scraperID, originalConfig)
		if err != nil {
			scraped[i].Error = err
			continue
		}
		all = all.Merge(extracted)

		for _, cr := range extracted.Changes {
			if cr.ExternalID == "" {
				cr.ExternalID = scraped[i].ID
			}

			if cr.ConfigType == "" {
				cr.ConfigType = scraped[i].Type
			}

			if cr.ExternalID == "" && cr.ConfigType == "" {
				continue
			}

			scraped[i].Changes = append(scraped[i].Changes, cr)
		}

		scraped[i].Config = extracted.Config
	}

	type entityAppender struct {
		hasItems bool
		apply    func(result *v1.ScrapeResult)
	}

	appenders := []entityAppender{
		{len(all.ExternalUsers) > 0, func(r *v1.ScrapeResult) { r.ExternalUsers = all.ExternalUsers }},
		{len(all.ExternalGroups) > 0, func(r *v1.ScrapeResult) { r.ExternalGroups = all.ExternalGroups }},
		{len(all.ExternalRoles) > 0, func(r *v1.ScrapeResult) { r.ExternalRoles = all.ExternalRoles }},
		{len(all.ExternalUserGroups) > 0, func(r *v1.ScrapeResult) { r.ExternalUserGroups = all.ExternalUserGroups }},
		{len(all.ConfigAccess) > 0, func(r *v1.ScrapeResult) { r.ConfigAccess = all.ConfigAccess }},
		{len(all.AccessLogs) > 0, func(r *v1.ScrapeResult) { r.ConfigAccessLogs = all.AccessLogs }},
	}

	for _, a := range appenders {
		if a.hasItems {
			result := v1.NewScrapeResult(scraped[0].BaseScraper)
			a.apply(result)
			scraped = append(scraped, *result)
		}
	}

	return scraped
}
