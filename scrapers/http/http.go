package http

import (
	"encoding/json"
	"fmt"
	"strings"

	commonsHTTP "github.com/flanksource/commons/http"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/gomplate/v3"
	"github.com/samber/lo"
)

func errorBody(response *commonsHTTP.Response) string {
	body, err := response.AsString()
	if err != nil || body == "" {
		return ""
	}
	if len(body) > 500 {
		body = body[:500]
	}
	return body
}

type Scraper struct{}

func (file Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.HTTP) > 0
}

func (file Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, spec := range ctx.ScrapeConfig().Spec.HTTP {
		scraped, err := scrape(ctx, spec)
		if err != nil {
			results = append(results, v1.ScrapeResult{Error: err})
		} else {
			results = append(results, scraped...)
		}
	}

	return results
}

func scrape(ctx api.ScrapeContext, spec v1.HTTP) (v1.ScrapeResults, error) {
	ctx.Logger.V(4).Infof("hydrating HTTP: %s", spec.HTTPConnection)

	conn, err := spec.HTTPConnection.Hydrate(ctx, ctx.Namespace())
	if err != nil {
		return nil, fmt.Errorf("failed to populate connection: %w", err)
	}
	if spec.URL == "" {
		return nil, fmt.Errorf("HTTP URL is empty")
	}

	ctx.Logger.V(3).Infof("scraping HTTP: %s", spec.HTTPConnection)

	client, err := connection.CreateHTTPClient(ctx, *conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	for _, header := range conn.Headers {
		if header.Name == "" {
			continue
		}
		if v, err := ctx.GetEnvValueFromCache(header, ctx.Namespace()); err != nil {
			return nil, fmt.Errorf("failed to get header env value for %v: %w", header, err)
		} else {
			client.Header(header.Name, v)
		}
	}

	templateEnv := map[string]any{}
	for _, env := range spec.Env {
		if v, err := ctx.GetEnvValueFromCache(env, ctx.Namespace()); err != nil {
			return nil, fmt.Errorf("failed to get env value for %v: %w", env, err)
		} else {
			templateEnv[env.Name] = v
		}
	}

	url, err := gomplate.RunTemplate(templateEnv, gomplate.Template{Template: conn.URL})
	if err != nil {
		return nil, fmt.Errorf("failed to apply template: %w", err)
	}

	if url == "" {
		return nil, fmt.Errorf("result of applying template is empty")
	}

	method := lo.CoalesceOrEmpty(lo.FromPtr(spec.Method), "GET")
	if ctx.IsTrace() {
		client = client.WithHttpLogging(logger.Debug, logger.Debug)
	} else if ctx.IsDebug() {
		client = client.WithHttpLogging(logger.Debug, logger.Trace1)
	}

	request := client.R(ctx)
	if spec.Body != nil {
		if err := request.Body(*spec.Body); err != nil {
			return nil, fmt.Errorf("failed to apply TLS config: %w", err)
		}
	}

	response, err := request.Do(method, url)
	if err != nil {
		return nil, fmt.Errorf("error calling URL: %w", err)
	}

	if !response.IsOK() {
		return nil, fmt.Errorf("request returned HTTP %d: %s", response.StatusCode, errorBody(response))
	}

	if spec.Pagination != nil && spec.Pagination.NextPageExpr != "" {
		return paginate(ctx, client, *spec.Pagination, response, url, spec.BaseScraper)
	}

	responseBody, err := response.AsString()
	if err != nil {
		return nil, fmt.Errorf("failed to get response as a string: %w", err)
	}

	result := v1.NewScrapeResult(spec.BaseScraper)
	if !response.IsJSON() {
		result.Config = responseBody
	} else {
		if strings.HasPrefix(responseBody, "[") {
			var jsonArr []any
			if err := json.Unmarshal([]byte(responseBody), &jsonArr); err != nil {
				return nil, fmt.Errorf("failed to unmarshal response: %w", err)
			}
			result.Config = jsonArr
		} else {
			var jsonObj map[string]any
			if err := json.Unmarshal([]byte(responseBody), &jsonObj); err != nil {
				return nil, fmt.Errorf("failed to unmarshal response: %w", err)
			}
			result.Config = jsonObj
		}
	}

	return v1.ScrapeResults{*result}, nil
}
