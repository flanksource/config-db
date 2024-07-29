package http

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flanksource/commons/http"
	"github.com/flanksource/commons/http/middlewares"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/gomplate/v3"
	"github.com/samber/lo"
)

type Scraper struct{}

func (file Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.HTTP) > 0
}

func (file Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, spec := range ctx.ScrapeConfig().Spec.HTTP {
		if result, err := scrape(ctx, spec); err != nil {
			results = append(results, v1.ScrapeResult{Error: err})
		} else {
			results = append(results, *result)
		}
	}

	return results
}

func scrape(ctx api.ScrapeContext, spec v1.HTTP) (*v1.ScrapeResult, error) {
	conn, err := spec.HTTPConnection.Hydrate(ctx, ctx.Namespace())
	if err != nil {
		return nil, fmt.Errorf("failed to populate connection: %w", err)
	}

	client := http.NewClient()
	if !conn.Authentication.IsEmpty() {
		client = client.Auth(conn.Authentication.Username.ValueStatic, conn.Authentication.Password.ValueStatic)
	}

	if !conn.Bearer.IsEmpty() {
		client = client.Header("Authorization", fmt.Sprintf("Bearer %s", conn.Bearer.ValueStatic))
	}

	if !conn.OAuth.IsEmpty() {
		client = client.OAuth(middlewares.OauthConfig{
			ClientID:     conn.OAuth.ClientID.ValueStatic,
			ClientSecret: conn.OAuth.ClientSecret.ValueStatic,
			TokenURL:     conn.OAuth.TokenURL,
			Scopes:       conn.OAuth.Scopes,
			Params:       conn.OAuth.Params,
		})
	}

	if !conn.TLS.IsEmpty() {
		client, err = client.TLSConfig(http.TLSConfig{
			InsecureSkipVerify: conn.TLS.InsecureSkipVerify,
			HandshakeTimeout:   conn.TLS.HandshakeTimeout,
			CA:                 conn.TLS.CA.ValueStatic,
			Cert:               conn.TLS.Cert.ValueStatic,
			Key:                conn.TLS.Key.ValueStatic,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to apply TLS config: %w", err)
		}
	}

	for key, val := range spec.Headers {
		if v, err := ctx.GetEnvValueFromCache(val, ctx.Namespace()); err != nil {
			return nil, fmt.Errorf("failed to get header env value for %v: %w", val, err)
		} else {
			client.Header(key, v)
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

	method := lo.CoalesceOrEmpty(lo.FromPtr(spec.Method), "GET")
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

	return result, nil
}
