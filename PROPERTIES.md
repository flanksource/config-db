# Properties Reference

All properties are read at runtime via `ctx.Properties()` or `commons/properties` and can be set in the `properties` database table.

## Retention & Cleanup

| Property                                 | Type     | Default | Description                                                        |
| ---------------------------------------- | -------- | ------- | ------------------------------------------------------------------ |
| `config.retention.period`                | Duration | `7d`    | How long to keep soft-deleted config items before hard deletion    |
| `config.retention.stale_item_age`        | Duration | `24h`   | Age after which unseen config items are marked stale               |
| `config_scraper.retention.period`        | Duration | `30d`   | How long to keep soft-deleted config scrapers before hard deletion |
| `config_analysis.retention.max_age`      | Duration | `48h`   | Max age for resolved analysis records before deletion              |
| `config_analysis.set_status_closed_days` | Int      | `7`     | Days after which resolved analysis status is set to closed         |

## Change Deduplication

| Property                | Type     | Default | Description                                            |
| ----------------------- | -------- | ------- | ------------------------------------------------------ |
| `changes.dedup.window`  | Duration | `1h`    | Time window for deduplicating identical config changes |
| `changes.dedup.disable` | Bool     | `false` | Disable change deduplication entirely                  |

## Scraper Concurrency

Controls the maximum number of concurrent scraper runs via semaphores.

| Property                             | Type | Default | Description                             |
| ------------------------------------ | ---- | ------- | --------------------------------------- |
| `scraper.concurrency`                | Int  | `12`    | Global max concurrent scrapers          |
| `scraper.aws.concurrency`            | Int  | `2`     | Max concurrent AWS scrapers             |
| `scraper.azure.concurrency`          | Int  | `2`     | Max concurrent Azure scrapers           |
| `scraper.azuredevops.concurrency`    | Int  | `5`     | Max concurrent Azure DevOps scrapers    |
| `scraper.file.concurrency`           | Int  | `10`    | Max concurrent file scrapers            |
| `scraper.gcp.concurrency`            | Int  | `2`     | Max concurrent GCP scrapers             |
| `scraper.githubactions.concurrency`  | Int  | `5`     | Max concurrent GitHub Actions scrapers  |
| `scraper.http.concurrency`           | Int  | `10`    | Max concurrent HTTP scrapers            |
| `scraper.kubernetes.concurrency`     | Int  | `3`     | Max concurrent Kubernetes scrapers      |
| `scraper.kubernetesfile.concurrency` | Int  | `3`     | Max concurrent Kubernetes file scrapers |
| `scraper.slack.concurrency`          | Int  | `5`     | Max concurrent Slack scrapers           |
| `scraper.sql.concurrency`            | Int  | `10`    | Max concurrent SQL scrapers             |
| `scraper.terraform.concurrency`      | Int  | `10`    | Max concurrent Terraform scrapers       |
| `scraper.trivy.concurrency`          | Int  | `1`     | Max concurrent Trivy scrapers           |
| `scraper.playwright.concurrency`     | Int  | `2`     | Max concurrent Playwright scrapers      |

## Scheduling

| Property                      | Type     | Default      | Description                                                     |
| ----------------------------- | -------- | ------------ | --------------------------------------------------------------- |
| `scrapers.default.schedule`   | String   | `@every 60m` | Default cron schedule for scrapers without an explicit schedule |
| `scraper.{uid}.schedule`      | String   | —            | Override schedule for a specific scraper by UID                 |
| `scraper.{type}.schedule.min` | Duration | `29s`        | Minimum allowed schedule interval per scraper type              |

## Timeouts

| Property          | Type     | Default | Description                                    |
| ----------------- | -------- | ------- | ---------------------------------------------- |
| `scraper.timeout` | Duration | `4h`    | Overall timeout for a single scraper execution |

## Diff Processing

| Property                     | Type | Default | Description                                            |
| ---------------------------- | ---- | ------- | ------------------------------------------------------ |
| `scraper.diff.disable`       | Bool | `false` | Disable diff generation for config changes             |
| `scraper.diff.timer.minSize` | Int  | `20480` | Min config size (bytes) to enable detailed diff timing |

## Log Levels

Log levels are managed via `commons/logger`. Supported values: `fatal`, `error`, `warn`, `info`, `debug`, `trace`, `trace1`..`trace9`.

| Property                 | Type   | Default        | Description                                                       |
| ------------------------ | ------ | -------------- | ----------------------------------------------------------------- |
| `log.level`              | String | `info`         | Root log level for all loggers                                    |
| `log.level.{name}`       | String | root level     | Per-logger level override (e.g. `log.level.db`, `log.level.http`) |
| `db.log.level`           | String | —              | Alias for `log.level.db` — sets the database logger level         |
| `log.json`               | Bool   | `false`        | Output logs in JSON format                                        |
| `log.color`              | Bool   | `true`         | Enable colored log output (auto-disabled when `log.json` is on)   |
| `log.color.{name}`       | Bool   | parent         | Per-logger color override                                         |
| `log.caller`             | Bool   | `false`        | Include caller file:line in log output                            |
| `log.caller.{name}`      | Bool   | parent         | Per-logger caller override                                        |
| `log.time.format`        | String | `15:04:05.000` | Go time format string for log timestamps                          |
| `log.time.format.{name}` | String | parent         | Per-logger time format override                                   |

## Database Logging

Properties from `duty/gorm` that control SQL query logging.

| Property               | Type     | Default | Description                                            |
| ---------------------- | -------- | ------- | ------------------------------------------------------ |
| `log.db.slowThreshold` | Duration | `1s`    | Queries slower than this are logged as warnings        |
| `log.db.params`        | Bool     | `false` | Log SQL query parameters (auto-enabled at trace level) |
| `log.db.maxLength`     | Int      | `1024`  | Max length of logged SQL query strings                 |

## Scraper-Scoped Logging

These properties are accessed via `ctx.PropertyOn(default, key)` which resolves as `scraper.{uid}.{key}` then `scraper.{key}`. They can be set globally or per-scraper.

| Property                          | Type     | Default | Description                                                    |
| --------------------------------- | -------- | ------- | -------------------------------------------------------------- |
| `scraper.log.items`               | Bool     | `false` | Log individual scraped items (adds, updates, deletes)          |
| `scraper.log.slow_diff_threshold` | Duration | `1s`    | Diff duration above which a SLOW DIFF warning is logged        |
| `scraper.log.transforms`          | Bool     | `false` | Log change rule transform results (requires debug level)       |
| `scraper.log.missing`             | Bool     | `false` | Log warnings for missing parents, relationships, and resources |
| `scraper.log.exclusions`          | Bool     | `false` | Log when items are excluded by scraper filters                 |
| `scraper.log.skipped`             | Bool     | `false` | Log when Kubernetes resources are skipped                      |
| `scraper.log.noResourceId`        | Bool     | `true`  | Log when Kubernetes resources have no resource ID              |
| `scraper.log.relationships`       | Bool     | `false` | Log relationship resolution details                            |
| `scraper.log.changes.unmatched`   | Bool     | `true`  | Log when config changes can't be matched to a config item      |
| `scraper.log.rule.expr`           | Bool     | `false` | Log change rule expression evaluation failures                 |
| `scraper.tag.missing`             | Bool     | `false` | Log when tag extraction fails for a config item                |
| `scraper.label.missing`           | Bool     | `false` | Log when label extraction fails for a config item              |

## Scraper Controls

Per-scraper feature flags, also resolved via `scraper.{uid}.{key}` / `scraper.{key}`.

| Property                           | Type | Default | Description                                            |
| ---------------------------------- | ---- | ------- | ------------------------------------------------------ |
| `scraper.disable`                  | Bool | `false` | Disable a scraper entirely                             |
| `scraper.runNow`                   | Bool | `false` | Run the scraper immediately on schedule registration   |
| `scraper.watch.disable`            | Bool | `false` | Disable Kubernetes watch/informer for a scraper        |
| `scraper.azure.devops.incremental` | Bool | `true`  | Enable incremental scraping for Azure DevOps pipelines |

## Event Processing

| Property                                 | Type     | Default | Description                                          |
| ---------------------------------------- | -------- | ------- | ---------------------------------------------------- |
| `scrapers.event.stale-timeout`           | Duration | `1h`    | Events older than this are discarded as stale        |
| `scrapers.event.workers`                 | Int      | `2`     | Number of concurrent event consumer goroutines       |
| `incremental_scrape_event.lag_threshold` | Duration | `30s`   | Threshold for logging slow incremental scrape events |

## Kubernetes

| Property                        | Type | Default | Description                                                     |
| ------------------------------- | ---- | ------- | --------------------------------------------------------------- |
| `kubernetes.rbac_config_access` | Bool | `true`  | Enable RBAC config access extraction during Kubernetes scraping |
| `kubernetes.get.concurrency`    | Int  | `10`    | Max concurrent Kubernetes API get requests for involved objects |

## AWS

| Property                                  | Type     | Default | Description                                                  |
| ----------------------------------------- | -------- | ------- | ------------------------------------------------------------ |
| `scraper.aws.trusted_advisor.minInterval` | Duration | `16h`   | Minimum interval between AWS Trusted Advisor check refreshes |

## Azure DevOps

| Property                         | Type     | Default | Description                                            |
| -------------------------------- | -------- | ------- | ------------------------------------------------------ |
| `azuredevops.pipeline.max_age`   | Duration | `7d`    | Max age for fetching Azure DevOps pipeline runs        |
| `azuredevops.concurrency`        | Int      | `5`     | Concurrency for Azure DevOps API calls within a scrape |
| `azuredevops.terminal_cache.ttl` | Duration | `1h`    | TTL for caching terminal-state pipeline runs           |

## GitHub Actions

| Property                             | Type     | Default | Description                               |
| ------------------------------------ | -------- | ------- | ----------------------------------------- |
| `scrapers.githubactions.concurrency` | Int      | `10`    | Concurrency for GitHub workflow API calls |
| `scrapers.githubactions.maxAge`      | Duration | `7d`    | Max age for fetching GitHub workflow runs |

## RBAC / Casbin

Properties from `duty/rbac` that control the Casbin policy engine.

| Property              | Type     | Default | Description                                                 |
| --------------------- | -------- | ------- | ----------------------------------------------------------- |
| `casbin.cache`        | Bool     | `true`  | Enable Casbin policy cache                                  |
| `casbin.cache.expiry` | Duration | `1m`    | Casbin policy cache expiry                                  |
| `casbin.log.level`    | Int      | `1`     | Casbin log verbosity (>= 2 enables Casbin internal logging) |

## Resource Selectors

| Property                     | Type   | Default | Description                                                         |
| ---------------------------- | ------ | ------- | ------------------------------------------------------------------- |
| `log.level.resourceSelector` | String | `""`    | When non-empty, enables trace logging for resource selector queries |

## Caching

| Property                 | Type     | Default | Description                                    |
| ------------------------ | -------- | ------- | ---------------------------------------------- |
| `external.cache.timeout` | Duration | `24h`   | TTL for external user/role/group entity caches |
