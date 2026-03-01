package devops

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/duration"
	"github.com/flanksource/commons/hash"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/flanksource/gomplate/v3"
	"github.com/lib/pq"
	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"
)

const (
	PipelineType = "AzureDevops::Pipeline"
	ReleaseType  = "AzureDevops::Release"
)

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
		since := effectiveSince(maxAge, ctx.ScrapeConfig().Status.LastRun.Timestamp.Time)

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
					pipelineResults := ado.scrapePipeline(ctx, client, config, project, pipeline, since, cutoff, cacheTTL, approvalsByRunID)
					mu.Lock()
					projectResults = append(projectResults, pipelineResults...)
					mu.Unlock()
					return nil
				})
			}
			_ = g.Wait()
			results = append(results, projectResults...)

			if len(config.Releases) > 0 {
				releaseClient, err := NewAzureDevopsReleaseClient(ctx, config)
				if err != nil {
					results.Errorf(err, "failed to create release client for %s", config.Organization)
				} else {
					results = append(results, ado.scrapeReleases(ctx, releaseClient, config, project, cutoff)...)
				}
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
	approvalsByRunID map[int][]PipelineApproval,
) v1.ScrapeResults {
	errs := &v1.ScrapeResults{}

	// Revision-based definition cache (FR-4)
	pipelineDef, cached := pipelineDefCache.get(pipeline.ID, pipeline.Revision)
	if !cached {
		var err error
		pipelineDef, err = client.GetPipelineWithDefinition(ctx, project.Name, pipeline.ID)
		if err != nil {
			errs.Errorf(err, "failed to get pipeline definition for %s/%s", project.Name, pipeline.Name)
			return *errs
		}
		pipelineDefCache.set(pipeline.ID, pipeline.Revision, pipelineDef)
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
	if err := terminalRunCache.ensureFresh(ctx, cacheTTL, project.Name, pipeline.ID); err != nil {
		ctx.Logger.V(4).Infof("failed to warm terminal-run cache for %s/%s: %v", project.Name, pipeline.Name, err)
	}

	externalUsers := make(map[string]dutyModels.ExternalUser)
	externalGroups := make(map[string]dutyModels.ExternalGroup)
	var accessLogs []v1.ExternalConfigAccessLog
	var configAccess []v1.ExternalConfigAccess

	// Fetch pipeline permissions if enabled and interval has passed
	if config.Permissions != nil && config.Permissions.Enabled {
		pipelineKey := fmt.Sprintf("%s/%s/%d", config.Organization, project.Name, pipeline.ID)
		if shouldFetchPermissions(pipelineKey, parsePermissionsInterval(config.Permissions.RateLimit)) {
			ctx.Logger.V(3).Infof("fetching permissions for %s/%s", project.Name, pipeline.Name)
			acls, err := client.GetPipelinePermissions(ctx, project.Name, project.ID, pipeline.ID)
			if err != nil {
				ctx.Logger.V(4).Infof("failed to get permissions for %s/%s: %v", project.Name, pipeline.Name, err)
			} else if len(acls) > 0 {
				permissions := ParsePermissions(acls)
				var descriptorsToResolve []string
				for _, perm := range permissions {
					if perm.CanQueue || perm.CanAdmin {
						descriptorsToResolve = append(descriptorsToResolve, perm.IdentityDescriptor)
					}
				}
				if len(descriptorsToResolve) > 0 {
					identities, err := client.GetIdentitiesByDescriptor(ctx, descriptorsToResolve)
					if err != nil {
						ctx.Logger.V(4).Infof("failed to resolve identities for %s/%s: %v", project.Name, pipeline.Name, err)
					} else {
						identityMap := make(map[string]ResolvedIdentity, len(identities))
						for _, id := range identities {
							identityMap[id.Descriptor] = id
						}
						for _, perm := range permissions {
							if !perm.CanQueue && !perm.CanAdmin {
								continue
							}
							identity, ok := identityMap[perm.IdentityDescriptor]
							if !ok {
								continue
							}
							var email string
							if props := identity.Properties; props != nil {
								if mailProp, ok := props["Mail"]; ok {
									email = mailProp.Value
								}
							}
							if identity.IsContainer {
								groupID, err := hash.DeterministicUUID(pq.StringArray{identity.Descriptor})
								if err != nil {
									continue
								}
								if _, exists := externalGroups[identity.Descriptor]; !exists {
									externalGroups[identity.Descriptor] = dutyModels.ExternalGroup{
										ID:        groupID,
										Name:      identity.ProviderDisplayName,
										Aliases:   pq.StringArray{identity.Descriptor, identity.SubjectDescriptor},
										AccountID: config.Organization,
										GroupType: "AzureDevOps",
									}
								}
								configAccess = append(configAccess, v1.ExternalConfigAccess{
									ConfigExternalID:     v1.ExternalID{ConfigType: PipelineType, ExternalID: fmt.Sprintf("%s/%d", project.Name, pipeline.ID)},
									ExternalGroupAliases: []string{identity.Descriptor},
								})
							} else {
								if email == "" {
									email = identity.ProviderDisplayName
								}
								userID, err := hash.DeterministicUUID(pq.StringArray{email})
								if err != nil {
									continue
								}
								if _, exists := externalUsers[email]; !exists {
									externalUsers[email] = dutyModels.ExternalUser{
										ID:        userID,
										Name:      identity.ProviderDisplayName,
										Email:     &email,
										Aliases:   pq.StringArray{email, identity.Descriptor, identity.SubjectDescriptor},
										AccountID: config.Organization,
										UserType:  "AzureDevOps",
									}
								}
								configAccess = append(configAccess, v1.ExternalConfigAccess{
									ConfigExternalID:    v1.ExternalID{ConfigType: PipelineType, ExternalID: fmt.Sprintf("%s/%d", project.Name, pipeline.ID)},
									ExternalUserAliases: []string{email},
								})
							}
						}
					}
				}
			}
			markPermissionsFetched(pipelineKey)
		} else {
			ctx.Logger.V(4).Infof("skipping permissions fetch for %s/%s (interval not reached)", project.Name, pipeline.Name)
		}
	}

	// Incremental build fetch (FR-3)
	builds, err := client.GetBuilds(ctx, project.Name, pipeline.ID, since) //nolint:govet
	if err != nil {
		ctx.Logger.V(4).Infof("failed to get builds for %s/%s: %v", project.Name, pipeline.Name, err)
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
		errs.Errorf(err, "failed to get pipeline runs for %s/%s", project.Name, pipeline.Name)
		return *errs
	}

	uniquePipelines := make(map[string]Pipeline) //nolint:govet

	for _, run := range runs {
		externalChangeID := fmt.Sprintf("%s/%d/%d", project.Name, pipeline.ID, run.ID)

		// Skip runs older than maxAge cutoff (FR-4)
		if run.CreatedDate.Before(cutoff) {
			ctx.Logger.V(5).Infof("skipping run %s: created %s before cutoff %s", externalChangeID, run.CreatedDate.Format(time.RFC3339), cutoff.Format(time.RFC3339))
			continue
		}

		// Skip terminal runs already stored in DB (FR-2 / FR-5)
		if terminalRunCache.has(externalChangeID) {
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
				errs.Errorf(err, "failed to render id template for %s/%s", project.Name, pipeline.Name)
				return *errs
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
				ctx.Logger.V(4).Infof("failed to get timeline for run %d: %v", run.ID, err)
			} else {
				webURL := ""
				if webLink, ok := run.Links["web"]; ok {
					webURL = webLink.Href
				}
				runDetails.Steps = GetJobStepsSummary(timeline, webURL)
			}

			artifacts, err := client.GetBuildArtifacts(ctx, project.Name, run.ID)
			if err != nil {
				ctx.Logger.V(4).Infof("failed to get artifacts for run %d: %v", run.ID, err)
			} else {
				runDetails.Artifacts = artifacts
			}

			tests, err := client.GetTestRuns(ctx, project.Name, run.ID)
			if err != nil {
				ctx.Logger.V(4).Infof("failed to get test runs for run %d: %v", run.ID, err)
			} else {
				runDetails.Tests = tests
			}

			// Mark terminal so future scrapes skip this run
			terminalRunCache.add(externalChangeID)
		}

		// Track requester as external user + access log
		if requester != nil && requester.UniqueName != "" {
			email := requester.UniqueName
			if _, exists := externalUsers[email]; !exists {
				userID, err := hash.DeterministicUUID(pq.StringArray{email})
				if err != nil {
					ctx.Logger.V(4).Infof("failed to generate user id for %s: %v", email, err)
				} else {
					externalUsers[email] = dutyModels.ExternalUser{
						ID:        userID,
						Name:      requester.DisplayName,
						Email:     &email,
						Aliases:   pq.StringArray{email, requester.ID},
						AccountID: config.Organization,
						UserType:  "AzureDevOps",
					}
				}
			}
			if user, ok := externalUsers[email]; ok {
				accessLogs = append(accessLogs, v1.ExternalConfigAccessLog{
					ConfigAccessLog: dutyModels.ConfigAccessLog{
						ExternalUserID: user.ID,
						CreatedAt:      run.CreatedDate,
					},
					ConfigExternalID: v1.ExternalID{
						ConfigType: PipelineType,
						ExternalID: fmt.Sprintf("%s/%d", project.Name, pipeline.ID),
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
				email := approver.UniqueName
				if _, exists := externalUsers[email]; !exists {
					userID, err := hash.DeterministicUUID(pq.StringArray{email})
					if err != nil {
						ctx.Logger.V(4).Infof("failed to generate user id for approver %s: %v", email, err)
						continue
					}
					externalUsers[email] = dutyModels.ExternalUser{
						ID:        userID,
						Name:      approver.DisplayName,
						Email:     &email,
						Aliases:   pq.StringArray{email, approver.ID},
						AccountID: config.Organization,
						UserType:  "AzureDevOps",
					}
				}
				if user, ok := externalUsers[email]; ok {
					accessLogs = append(accessLogs, v1.ExternalConfigAccessLog{
						ConfigAccessLog: dutyModels.ConfigAccessLog{
							ExternalUserID: user.ID,
							CreatedAt:      step.LastModifiedOn,
						},
						ConfigExternalID: v1.ExternalID{
							ConfigType: PipelineType,
							ExternalID: fmt.Sprintf("%s/%d", project.Name, pipeline.ID),
						},
					})
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

		changeResult := v1.ChangeResult{
			ChangeType:       changeType,
			CreatedAt:        &run.CreatedDate,
			Severity:         severity,
			ExternalID:       id,
			ConfigType:       PipelineType,
			Source:           run.Links["web"].Href,
			Summary:          summary,
			Details:          runDetails.ToJSON(),
			ExternalChangeID: externalChangeID,
		}
		if requester != nil {
			changeResult.CreatedBy = lo.ToPtr(requester.UniqueName)
		}

		localPipeline.Runs = append(localPipeline.Runs, changeResult)
		uniquePipelines[id] = localPipeline
	}

	var pipelineResults v1.ScrapeResults
	for id, p := range uniquePipelines {
		changes := p.Runs
		p.Runs = nil

		pipelineConfig := buildPipelineConfig(p)
		configJSON, _ := json.Marshal(pipelineConfig)
		var configMap map[string]any
		json.Unmarshal(configJSON, &configMap)

		users := make([]dutyModels.ExternalUser, 0, len(externalUsers))
		for _, u := range externalUsers {
			users = append(users, u)
		}
		groups := make([]dutyModels.ExternalGroup, 0, len(externalGroups))
		for _, g := range externalGroups {
			groups = append(groups, g)
		}

		pipelineResults = append(pipelineResults, v1.ScrapeResult{
			ConfigClass:      "Deployment",
			Config:           configMap,
			Type:             PipelineType,
			ID:               id,
			Labels:           p.GetLabels(),
			Name:             p.Name,
			Changes:          changes,
			Aliases:          []string{fmt.Sprintf("%s/%d", project.Name, pipeline.ID)},
			ExternalUsers:    users,
			ExternalGroups:   groups,
			ConfigAccess:     configAccess,
			ConfigAccessLogs: accessLogs,
		})
	}
	return pipelineResults
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
	if len(p.Variables) > 0 {
		vars := make(map[string]string, len(p.Variables))
		for k, v := range p.Variables {
			vars[k] = v.Value
		}
		cfg["variables"] = vars
	}
	if len(p.TemplateParameters) > 0 {
		cfg["templateParameters"] = p.TemplateParameters
	}
	if p.Links != nil {
		if web, ok := p.Links["web"]; ok {
			cfg["webUrl"] = web.Href
		}
	}
	return cfg
}
