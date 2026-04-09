package devops

import (
	"fmt"
	"sync"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/duration"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/azure"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/gomplate/v3"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"
)

const (
	PipelineType = "AzureDevops::Pipeline"
	ReleaseType  = "AzureDevops::Release"
)

func PipelineExternalID(organization, project string, pipelineID int) string {
	return fmt.Sprintf("azuredevops://%s/%s/pipeline/%d", organization, project, pipelineID)
}

// permissionCache stores the last time permissions were fetched for each pipeline
var permissionCache = struct {
	sync.RWMutex
	lastFetched map[string]time.Time
}{
	lastFetched: make(map[string]time.Time),
}

func shouldFetchPermissions(pipelineKey string, interval time.Duration) bool {
	permissionCache.RLock()
	lastFetch, exists := permissionCache.lastFetched[pipelineKey]
	permissionCache.RUnlock()
	return !exists || time.Since(lastFetch) >= interval
}

func markPermissionsFetched(pipelineKey string) {
	permissionCache.Lock()
	permissionCache.lastFetched[pipelineKey] = time.Now()
	permissionCache.Unlock()
}

func parsePermissionsInterval(intervalStr string) time.Duration {
	if intervalStr == "" {
		return 24 * time.Hour
	}
	d, err := time.ParseDuration(intervalStr)
	if err != nil {
		return 24 * time.Hour
	}
	return d
}

func resolveMaxAge(config v1.AzureDevops, ctx api.ScrapeContext) time.Duration {
	if config.MaxAge != "" {
		if d, err := duration.ParseDuration(config.MaxAge); err == nil && d > 0 {
			return time.Duration(d)
		}
	}
	return ctx.Properties().Duration("azuredevops.pipeline.max_age", 7*24*time.Hour)
}

// effectiveSince returns the oldest time that pipeline runs should be fetched from.
// It ensures since is never earlier than now-maxAge, even on the first scrape.
func effectiveSince(maxAge time.Duration, lastRun time.Time) time.Time {
	cutoff := time.Now().Add(-maxAge)
	if !lastRun.IsZero() && lastRun.After(cutoff) {
		return lastRun
	}
	return cutoff
}

type AzureDevopsScraper struct{}

func (ado AzureDevopsScraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.AzureDevops) > 0
}

func (ado AzureDevopsScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	concurrency := ctx.Properties().Int("azuredevops.concurrency", 5)
	cacheTTL := ctx.Properties().Duration("azuredevops.terminal_cache.ttl", time.Hour)

	results := v1.ScrapeResults{}
	for _, config := range ctx.ScrapeConfig().Spec.AzureDevops {
		if err := ctx.Err(); err != nil {
			return results
		}

		client, err := NewAzureDevopsClient(ctx, config)
		if err != nil {
			results.Errorf(err, "failed to create azure devops client for %s", config.Organization)
			continue
		}

		projects, err := client.GetProjects(ctx)
		if err != nil {
			results.Errorf(err, "failed to get projects for %s", config.Organization)
			continue
		}

		// Determine since time: always bounded by maxAge, capped to LastRun when more recent
		maxAge := resolveMaxAge(config, ctx)
		cutoff := time.Now().Add(-maxAge)
		incremental := ctx.PropertyOn(true, "azure.devops.incremental")
		since := cutoff
		if incremental {
			since = effectiveSince(maxAge, ctx.ScrapeConfig().Status.LastRun.Timestamp.Time)
		} else {
			ctx.Logger.V(3).Infof("azure.devops.incremental=false, performing full scan (maxAge=%s)", maxAge)
		}

		for _, project := range projects {
			if err := ctx.Err(); err != nil {
				return results
			}
			if !collections.MatchItems(project.Name, config.Projects...) {
				continue
			}

			pipelines, err := client.GetPipelines(ctx, project.Name)
			if err != nil {
				results.Errorf(err, "failed to get pipelines for %s", project.Name)
				continue
			}
			ctx.Logger.V(3).Infof("[%s] found %d pipelines", project.Name, len(pipelines))

			// Fetch all approvals for the project once — O(1) instead of O(runs)
			approvalsByRunID, err := client.GetProjectApprovals(ctx, project.Name)
			if err != nil {
				ctx.Logger.V(4).Infof("failed to get project approvals for %s: %v", project.Name, err)
				approvalsByRunID = map[int][]PipelineApproval{}
			}

			// Share a single entity context per project so users/groups are deduped
			entityCtx := ctx.WithEntities()

			// Fan-out pipeline processing with a bounded worker pool
			var (
				mu             sync.Mutex
				projectResults v1.ScrapeResults
			)
			g, _ := errgroup.WithContext(ctx)
			g.SetLimit(concurrency)

			for _, _pipeline := range pipelines {
				pipeline := _pipeline
				if !collections.MatchItems(pipeline.Name, config.Pipelines...) {
					continue
				}

				g.Go(func() error {
					if err := ctx.Err(); err != nil {
						return nil // context cancelled, stop silently
					}
					pipelineResults := ado.scrapePipeline(entityCtx, client, config, project, pipeline, since, cutoff, cacheTTL, incremental, approvalsByRunID)
					mu.Lock()
					projectResults = append(projectResults, pipelineResults...)
					mu.Unlock()
					return nil
				})
			}
			_ = g.Wait()

			if len(config.Releases) > 0 {
				releaseClient, err := NewAzureDevopsReleaseClient(ctx, config)
				if err != nil {
					return results.Errorf(err, "failed to create release client for %s", config.Organization)
				} else {
					projectResults = append(projectResults, ado.scrapeReleases(entityCtx, client, releaseClient, config, project, cutoff)...)
				}
			}

			if len(config.Repositories) > 0 {
				projectResults = append(projectResults, ado.scrapeRepositories(ctx, client, config, project)...)
			}

			// Assign deduped entities only to the first result to avoid duplicates
			if len(projectResults) > 0 {
				projectResults[0].ExternalUsers = entityCtx.Users()
				projectResults[0].ExternalGroups = entityCtx.Groups()
			}
			results = append(results, projectResults...)
		}

		if config.AuditLog != nil && config.AuditLog.Enabled {
			results = append(results, ado.scrapeAuditLog(ctx, config)...)
		}

		if config.Permissions != nil && config.Permissions.Groups {
			groupsKey := "groups/" + config.Organization
			if shouldFetchPermissions(groupsKey, parsePermissionsInterval(config.Permissions.RateLimit)) {
				results = append(results, ado.scrapeGroups(ctx, client, config)...)
				markPermissionsFetched(groupsKey)
			}
		}
	}
	return results
}

// scrapePipeline processes a single pipeline and returns its ScrapeResults.
func (ado AzureDevopsScraper) scrapePipeline(
	ctx api.ScrapeContext,
	client *AzureDevopsClient,
	config v1.AzureDevops,
	project Project,
	pipeline Pipeline,
	since time.Time,
	cutoff time.Time,
	cacheTTL time.Duration,
	incremental bool,
	approvalsByRunID map[int][]PipelineApproval,
) v1.ScrapeResults {

	var results v1.ScrapeResults

	// Revision-based definition cache (FR-4)
	pipelineDef, cached := pipelineDefCache.get(config.Organization, project.Name, pipeline.ID, pipeline.Revision)
	if !cached {
		var err error
		pipelineDef, err = client.GetPipelineWithDefinition(ctx, project.Name, pipeline.ID)
		if err != nil {
			return results.Errorf(err, "failed to get pipeline definition for %s/%s", project.Name, pipeline.Name)
		}
		pipelineDefCache.set(config.Organization, project.Name, pipeline.ID, pipeline.Revision, pipelineDef)
	}

	pipeline = pipelineDef.Pipeline
	pipeline.Configuration = &PipelineConfig{
		Type: "yaml",
		Path: pipelineDef.YamlPath,
		Repository: &Repository{
			Name:          pipelineDef.RepositoryName,
			URL:           pipelineDef.RepositoryURL,
			DefaultBranch: pipelineDef.DefaultBranch,
		},
	}

	// Warm terminal-run cache for this pipeline (FR-2)
	if err := terminalRunCache.ensureFresh(ctx, cacheTTL, config.Organization, project.Name, pipeline.ID); err != nil {
		ctx.Logger.V(4).Infof("failed to warm terminal-run cache for %s/%s: %v", project.Name, pipeline.Name, err)
	}

	var accessLogs []v1.ExternalConfigAccessLog
	var configAccess []v1.ExternalConfigAccess
	var pipelineRoles []dutyModels.ExternalRole
	pipelineConfigExternalID := PipelineExternalID(config.Organization, project.Name, pipeline.ID)

	// Fetch pipeline permissions if enabled and interval has passed
	if config.Permissions != nil && config.Permissions.Enabled {
		pipelineKey := fmt.Sprintf("%s/%s/%d", config.Organization, project.Name, pipeline.ID)
		if shouldFetchPermissions(pipelineKey, parsePermissionsInterval(config.Permissions.RateLimit)) {
			ca, roles := ado.fetchPipelinePermissions(ctx, client, config, project, pipeline.ID, pipelineConfigExternalID)
			configAccess = ca
			pipelineRoles = roles
			markPermissionsFetched(pipelineKey)
		} else {
			ctx.Logger.V(4).Infof("skipping permissions fetch for %s/%s (interval not reached)", project.Name, pipeline.Name)
		}
	}

	// Incremental build fetch (FR-3)
	builds, err := client.GetBuilds(ctx, project.Name, pipeline.ID, since) //nolint:govet
	if err != nil {
		return results.Errorf(err, "failed to get builds for %s/%s", project.Name, pipeline.Name)
	}
	buildRequesters := make(map[int]*IdentityRef, len(builds))
	for _, build := range builds {
		switch {
		case build.RequestedFor != nil:
			buildRequesters[build.ID] = build.RequestedFor
		case build.RequestedBy != nil:
			buildRequesters[build.ID] = build.RequestedBy
		}
	}

	runs, err := client.GetPipelineRuns(ctx, project.Name, pipeline)
	if err != nil {
		return results.Errorf(err, "failed to get pipeline runs for %s/%s", project.Name, pipeline.Name)
	}

	uniquePipelines := make(map[string]Pipeline) //nolint:govet

	// Seed with base pipeline so a config item is always emitted even if all runs are skipped
	basePipeline := pipeline
	delete(basePipeline.Links, "self")
	baseID := basePipeline.GetID()
	if config.ID != "" {
		env := map[string]any{
			"project":      project,
			"pipeline":     basePipeline,
			"organization": config.Organization,
		}
		baseID, _ = gomplate.RunTemplate(env, gomplate.Template{Expression: config.ID})
	}
	uniquePipelines[baseID] = basePipeline

	for _, run := range runs {
		externalChangeID := fmt.Sprintf("%s/%s/%d/%d", config.Organization, project.Name, pipeline.ID, run.ID)

		// Skip runs older than maxAge cutoff (FR-4)
		if run.CreatedDate.Before(cutoff) {
			ctx.Logger.V(5).Infof("skipping run %s: created %s before cutoff %s", externalChangeID, run.CreatedDate.Format(time.RFC3339), cutoff.Format(time.RFC3339))
			continue
		}

		// Skip terminal runs already stored in DB (FR-2 / FR-5)
		if incremental && terminalRunCache.has(externalChangeID) {
			ctx.Logger.V(5).Infof("skipping terminal run %s", externalChangeID)
			continue
		}

		var localPipeline = pipeline
		localPipeline.TemplateParameters = run.TemplateParameters
		localPipeline.Variables = run.Variables
		delete(localPipeline.Links, "self")

		id := localPipeline.GetID()
		if config.ID != "" {
			env := map[string]any{
				"project":      project,
				"pipeline":     localPipeline,
				"organization": config.Organization,
			}
			id, err = gomplate.RunTemplate(env, gomplate.Template{Expression: config.ID})
			if err != nil {
				return results.Errorf(err, "failed to render id template for %s/%s", project.Name, pipeline.Name)
			}
		}

		if existing, ok := uniquePipelines[id]; ok {
			localPipeline = existing
		} else {
			uniquePipelines[id] = localPipeline
		}

		requester := buildRequesters[run.ID]
		terminal := isTerminalRun(run)

		approvals := approvalsByRunID[run.ID]
		hasPendingApproval := hasPendingApprovals(approvals)
		changeType := runChangeType(run, hasPendingApproval)

		parameters := make(map[string]any, len(run.TemplateParameters)+len(run.Variables))
		for k, v := range run.TemplateParameters {
			parameters[k] = v
		}
		for k, v := range run.Variables {
			parameters[k] = v.Value
		}

		runDetails := RunDetails{
			Run:         run,
			Parameters:  parameters,
			RequestedBy: requester,
			Approvals:   approvals,
		}

		// Full enrichment only on first terminal transition (FR-5)
		if terminal {
			timeline, err := client.GetBuildTimeline(ctx, project.Name, run.ID)
			if err != nil {
				return results.Errorf(err, "failed to get timeline for run %d in %s/%s", run.ID, project.Name, pipeline.Name)
			} else {
				webURL := ""
				if webLink, ok := run.Links["web"]; ok {
					webURL = webLink.Href
					runDetails.URL = webURL
				}

				runDetails.Steps = GetJobStepsSummary(timeline, webURL)
			}

			artifacts, err := client.GetBuildArtifacts(ctx, project.Name, run.ID)
			if err != nil {
				return results.Errorf(err, "failed to get artifacts for run %d in %s/%s", run.ID, project.Name, pipeline.Name)
			} else {
				runDetails.Artifacts = artifacts
			}

			tests, err := client.GetTestRuns(ctx, project.Name, run.ID)
			if err != nil {
				return results.Errorf(err, "failed to get test runs for run %d in %s/%s", run.ID, project.Name, pipeline.Name)
			} else {
				runDetails.Tests = tests
			}

			// Mark terminal so future scrapes skip this run
			terminalRunCache.add(externalChangeID)
		}

		if config.Permissions != nil && config.Permissions.Enabled {
			// Track requester as external user + access log
			if requester != nil && requester.UniqueName != "" {
				addExternalEntity(ctx, requester, config.Organization)
				if user := findUserByAlias(ctx.Users(), requester.UniqueName); user != nil {
					accessLogs = append(accessLogs, v1.ExternalConfigAccessLog{
						ConfigAccessLog: dutyModels.ConfigAccessLog{
							CreatedAt: run.CreatedDate,
						},
						ExternalUserAliases: user.Aliases,
						ConfigExternalID: v1.ExternalID{
							ConfigType: PipelineType,
							ExternalID: pipelineConfigExternalID,
						},
					})
				}
			}

			// Enrich approvers as external users + access logs
			for _, approval := range approvals {
				for _, step := range approval.Steps {
					approver := step.ActualApprover
					if approver == nil {
						approver = &step.AssignedApprover
					}
					if approver.UniqueName == "" {
						continue
					}
					addExternalEntity(ctx, approver, config.Organization)
					if user := findUserByAlias(ctx.Users(), approver.UniqueName); user != nil {
						accessLogs = append(accessLogs, v1.ExternalConfigAccessLog{
							ConfigAccessLog: dutyModels.ConfigAccessLog{
								CreatedAt: step.LastModifiedOn,
							},
							ExternalUserAliases: user.Aliases,
							ConfigExternalID: v1.ExternalID{
								ConfigType: PipelineType,
								ExternalID: pipelineConfigExternalID,
							},
						})
					}
				}
			}
		}

		delete(run.Links, "pipeline")
		delete(run.Links, "pipeline.web")

		severity := "info"
		if run.Result != RunResultSucceeded && run.Result != "" {
			severity = "failed"
		}

		summary := fmt.Sprintf("%s, %s in %s", run.Name, run.State, time.Millisecond*time.Duration(run.Duration))
		jobCount, taskCount := countSteps(runDetails.Steps)
		if jobCount > 0 || taskCount > 0 {
			summary = fmt.Sprintf("%s (%d jobs, %d tasks)", summary, jobCount, taskCount)
		}

		runDetails.URL = run.Links["web"].Href
		createdAt := run.CreatedDate
		changeResult := v1.ChangeResult{
			ChangeType:       changeType,
			CreatedAt:        &createdAt,
			Severity:         severity,
			ExternalID:       pipelineConfigExternalID,
			ConfigType:       PipelineType,
			Source:           "AzureDevops/pipeline/" + pipelineConfigExternalID,
			Summary:          summary,
			Details:          runDetails.ToJSON(),
			ExternalChangeID: externalChangeID,
		}
		if requester != nil {
			changeResult.CreatedBy = lo.ToPtr(requester.UniqueName)
		}

		localPipeline.Runs = append(localPipeline.Runs, changeResult)
		localPipeline.Runs = append(localPipeline.Runs, pipelineApprovalChanges(approvals, pipelineConfigExternalID, changeResult.Source, externalChangeID)...)
		uniquePipelines[id] = localPipeline
	}

	for id, p := range uniquePipelines {
		changes := p.Runs
		p.Runs = nil

		var configData any
		var format string
		if pipelineDef.YamlContent != "" {
			configData = pipelineDef.YamlContent
			format = "yaml"
		} else {
			configData = buildPipelineConfig(p)
		}

		var properties types.Properties
		if p.Configuration != nil && p.Configuration.Path != "" {
			properties = append(properties, &types.Property{
				Name: "Path",
				Text: p.Configuration.Path,
			})
		}
		if web, ok := p.Links["web"]; ok && web.Href != "" {
			properties = append(properties, &types.Property{
				Name:  "Source",
				Links: []types.Link{{Type: "source", URL: web.Href}},
			})
		}

		var aliases []string
		if id != pipelineConfigExternalID {
			aliases = []string{id}
		}

		results = append(results, v1.ScrapeResult{
			BaseScraper:      config.BaseScraper,
			ConfigClass:      "Deployment",
			Config:           configData,
			Format:           format,
			Type:             PipelineType,
			ID:               pipelineConfigExternalID,
			Labels:           p.GetLabels(),
			Name:             p.Name,
			Changes:          changes,
			Properties:       properties,
			Aliases:          aliases,
			ConfigAccess:     configAccess,
			ConfigAccessLogs: accessLogs,
			ExternalRoles:   pipelineRoles,
		})
	}
	return results
}

func (ado AzureDevopsScraper) fetchPipelinePermissions(
	ctx api.ScrapeContext,
	client *AzureDevopsClient,
	config v1.AzureDevops,
	project Project,
	pipelineID int,
	pipelineConfigExternalID string,
) ([]v1.ExternalConfigAccess, []dutyModels.ExternalRole) {
	acls, err := client.GetPipelinePermissions(ctx, project.Name, project.ID, pipelineID)
	if err != nil {
		ctx.Logger.V(4).Infof("failed to get pipeline permissions for %s/%d: %v", project.Name, pipelineID, err)
		return nil, nil
	}

	perms := ParseBuildPermissions(acls)
	if len(perms) == 0 {
		return nil, nil
	}

	var descriptors []string
	for _, p := range perms {
		descriptors = append(descriptors, p.IdentityDescriptor)
	}

	identities, err := client.GetIdentitiesByDescriptor(ctx, descriptors)
	if err != nil {
		ctx.Logger.V(4).Infof("failed to resolve identities for pipeline %s/%d: %v", project.Name, pipelineID, err)
		return nil, nil
	}

	identityMap := BuildIdentityMap(identities)

	roleIDs := make(map[string]uuid.UUID)
	var roles []dutyModels.ExternalRole
	var configAccess []v1.ExternalConfigAccess

	for _, perm := range perms {
		identity, ok := identityMap[perm.IdentityDescriptor]
		if !ok {
			continue
		}

		name := ResolvedIdentityName(identity, project.Name)
		email := emailFromIdentity(identity)
		if name == "" && email == "" {
			continue
		}

		if identity.IsContainer {
			aliases := append(DescriptorAliases(identity.Descriptor), identity.SubjectDescriptor)
			aliases = append(aliases, DescriptorAliases(identity.SubjectDescriptor)...)
			// No ID — the SQL merge resolves this group against the AAD scraper's
			// authoritative record by alias overlap. AAD takes precedence.
			ctx.AddGroup(dutyModels.ExternalGroup{
				Name:      name,
				Aliases:   pq.StringArray(aliases),
				Tenant:    config.Organization,
				GroupType: "AzureDevOps",
			})
		} else {
			ctx.AddUser(dutyModels.ExternalUser{
				Name:     name,
				Email:    &email,
				Aliases:  pq.StringArray{email, identity.Descriptor, identity.SubjectDescriptor},
				Tenant:   config.Organization,
				UserType: "AzureDevOps",
			})
		}

		resolvedRoles := ResolveRoles("Pipeline", perm.Permissions, config.Permissions.Roles)
		for _, roleName := range resolvedRoles {
			if _, exists := roleIDs[roleName]; !exists {
				roleID := azure.RoleID(ctx.ScraperID(), roleName)
				roleIDs[roleName] = roleID
				roles = append(roles, dutyModels.ExternalRole{
					ID:       roleID,
					Name:     roleName,
					RoleType: "AzureDevOps",
					Tenant:   config.Organization,
				})
			}

			roleID := roleIDs[roleName]
			access := v1.ExternalConfigAccess{
				ConfigExternalID: v1.ExternalID{ConfigType: PipelineType, ExternalID: pipelineConfigExternalID},
				ExternalRoleID:   &roleID,
			}
			if identity.IsContainer {
				access.ExternalGroupAliases = DescriptorAliases(identity.Descriptor)
			} else {
				access.ExternalUserAliases = []string{email}
			}
			configAccess = append(configAccess, access)
		}
	}

	return configAccess, roles
}

// hasPendingApprovals returns true if any approval step is neither approved nor rejected.
func hasPendingApprovals(approvals []PipelineApproval) bool {
	for _, a := range approvals {
		for _, step := range a.Steps {
			if step.Status != "approved" && step.Status != "rejected" {
				return true
			}
		}
	}
	return false
}

func countSteps(steps []JobStepSummary) (jobs, tasks int) {
	for _, s := range steps {
		switch s.Type {
		case "Job":
			jobs++
		case "Task":
			tasks++
		}
	}
	return
}

func pipelineApprovalChanges(approvals []PipelineApproval, externalID, source, baseExternalChangeID string) []v1.ChangeResult {
	var out []v1.ChangeResult
	for _, approval := range approvals {
		for i, step := range approval.Steps {
			var changeType string
			switch step.Status {
			case "approved":
				changeType = ChangeTypeApproved
			case "rejected":
				changeType = ChangeTypeRejected
			default:
				continue
			}

			approver := step.ActualApprover
			if approver == nil {
				approver = &step.AssignedApprover
			}

			severity := "info"
			summary := fmt.Sprintf("%s by %s", changeType, approver.UniqueName)
			if changeType == ChangeTypeRejected {
				severity = "high"
			}
			if step.Comment != "" {
				summary += ": " + step.Comment
			}

			createdAt := step.LastModifiedOn
			out = append(out, v1.ChangeResult{
				ChangeType:       changeType,
				CreatedAt:        &createdAt,
				CreatedBy:        lo.ToPtr(approver.UniqueName),
				Severity:         severity,
				ExternalID:       externalID,
				ConfigType:       PipelineType,
				Source:           source,
				Summary:          summary,
				ExternalChangeID: fmt.Sprintf("%s/approval/%s/%d", baseExternalChangeID, approval.ID, i),
			})
		}
	}
	return out
}

func buildPipelineConfig(p Pipeline) map[string]any {
	cfg := map[string]any{
		"id":       p.ID,
		"name":     p.Name,
		"url":      p.URL,
		"folder":   p.Folder,
		"revision": p.Revision,
	}
	if p.Configuration != nil {
		cfg["configuration"] = map[string]any{
			"type":     p.Configuration.Type,
			"yamlPath": p.Configuration.Path,
		}
		if p.Configuration.Repository != nil {
			cfg["repository"] = map[string]any{
				"name":          p.Configuration.Repository.Name,
				"url":           p.Configuration.Repository.URL,
				"defaultBranch": p.Configuration.Repository.DefaultBranch,
			}
		}
	}
	if p.Links != nil {
		if web, ok := p.Links["web"]; ok {
			cfg["webUrl"] = web.Href
		}
	}
	return cfg
}
