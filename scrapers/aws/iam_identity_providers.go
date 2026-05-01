package aws

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/types"
	"github.com/samber/lo"
)

func awsIAMOIDCProviderAlias(accountID, name string) string {
	return fmt.Sprintf("aws://iam-oidc-provider/%s/%s", accountID, name)
}

func awsIAMSAMLProviderAlias(accountID, name string) string {
	return fmt.Sprintf("aws://iam-saml-provider/%s/%s", accountID, name)
}

// iamOIDCProviders scrapes IAM OIDC identity providers. Gated on
// Includes("OIDCProviders"). Emits AWS::IAM::OIDCProvider config items
// with the provider ARN as primary alias so existing trust-policy
// ExternalConfigAccess rows (emitted from classifyFederated) resolve to
// these config items at SaveResults.
func (aws Scraper) iamOIDCProviders(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("OIDCProviders") {
		return
	}
	ctx.Logger.V(2).Infof("scraping IAM OIDC providers")

	listOut, err := ctx.IAM.ListOpenIDConnectProviders(ctx, &iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		results.Errorf(err, "failed to list IAM OIDC providers")
		return
	}

	accountID := lo.FromPtr(ctx.Caller.Account)

	for _, entry := range listOut.OpenIDConnectProviderList {
		arn := lo.FromPtr(entry.Arn)
		if arn == "" {
			continue
		}
		name := oidcProviderNameFromARN(arn)

		getOut, err := ctx.IAM.GetOpenIDConnectProvider(ctx, &iam.GetOpenIDConnectProviderInput{
			OpenIDConnectProviderArn: entry.Arn,
		})
		if err != nil {
			results.Errorf(err, "failed to get IAM OIDC provider %s", arn)
			continue
		}

		// URL host is the display name used by classifyFederated; prefer it so
		// the config-item Name matches the ExternalUser Name emitted for the
		// same provider in trust policies.
		if u := lo.FromPtr(getOut.Url); u != "" {
			name = oidcHostFromURL(u)
		}

		tags := map[string]string{}
		for _, t := range getOut.Tags {
			tags[lo.FromPtr(t.Key)] = lo.FromPtr(t.Value)
		}
		if config.ShouldExclude(v1.AWSIAMOIDCProvider, name, tags) {
			continue
		}

		cfg, err := utils.ToJSONMap(getOut)
		if err != nil {
			results.Errorf(err, "failed to convert OIDC provider to json")
			continue
		}

		sr := v1.ScrapeResult{
			Type:        v1.AWSIAMOIDCProvider,
			CreatedAt:   getOut.CreateDate,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSIAMOIDCProvider, arn, nil)},
			Config:      cfg,
			ConfigClass: "OIDCProvider",
			Name:        name,
			Aliases: []string{
				arn,
				awsIAMOIDCProviderAlias(accountID, name),
			},
			ID:      arn,
			Parents: []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: accountID}},
		}
		*results = append(*results, sr)
	}
}

// iamSAMLProviders scrapes IAM SAML identity providers. Gated on
// Includes("SAMLProviders"). Mirrors iamOIDCProviders so existing
// trust-policy access rows resolve to these config items.
func (aws Scraper) iamSAMLProviders(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes("SAMLProviders") {
		return
	}
	ctx.Logger.V(2).Infof("scraping IAM SAML providers")

	listOut, err := ctx.IAM.ListSAMLProviders(ctx, &iam.ListSAMLProvidersInput{})
	if err != nil {
		results.Errorf(err, "failed to list IAM SAML providers")
		return
	}

	accountID := lo.FromPtr(ctx.Caller.Account)

	for _, entry := range listOut.SAMLProviderList {
		arn := lo.FromPtr(entry.Arn)
		if arn == "" {
			continue
		}
		name := samlProviderNameFromARN(arn)

		getOut, err := ctx.IAM.GetSAMLProvider(ctx, &iam.GetSAMLProviderInput{
			SAMLProviderArn: entry.Arn,
		})
		if err != nil {
			results.Errorf(err, "failed to get IAM SAML provider %s", arn)
			continue
		}

		tags := map[string]string{}
		for _, t := range getOut.Tags {
			tags[lo.FromPtr(t.Key)] = lo.FromPtr(t.Value)
		}
		if config.ShouldExclude(v1.AWSIAMSAMLProvider, name, tags) {
			continue
		}

		cfg, err := utils.ToJSONMap(getOut)
		if err != nil {
			results.Errorf(err, "failed to convert SAML provider to json")
			continue
		}

		sr := v1.ScrapeResult{
			Type:        v1.AWSIAMSAMLProvider,
			CreatedAt:   getOut.CreateDate,
			BaseScraper: config.BaseScraper,
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSIAMSAMLProvider, arn, nil)},
			Config:      cfg,
			ConfigClass: "SAMLProvider",
			Name:        name,
			Aliases: []string{
				arn,
				awsIAMSAMLProviderAlias(accountID, name),
			},
			ID:      arn,
			Parents: []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: accountID}},
		}
		*results = append(*results, sr)
	}
}

// oidcProviderNameFromARN extracts the name segment from
// "arn:aws:iam::<account>:oidc-provider/<name>". Returns the full ARN on
// parse failure so callers still have a non-empty name.
func oidcProviderNameFromARN(arn string) string {
	if _, name, ok := strings.Cut(arn, ":oidc-provider/"); ok {
		return name
	}
	return arn
}

func samlProviderNameFromARN(arn string) string {
	if _, name, ok := strings.Cut(arn, ":saml-provider/"); ok {
		return name
	}
	return arn
}

// oidcHostFromURL returns the host portion of an OIDC issuer URL, matching
// what classifyFederated emits as the display name for federated principals.
func oidcHostFromURL(issuer string) string {
	if u, err := url.Parse(issuer); err == nil && u.Host != "" {
		return u.Host
	}
	// Strip scheme manually if url.Parse failed.
	s := strings.TrimPrefix(issuer, "https://")
	s = strings.TrimPrefix(s, "http://")
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	return s
}
