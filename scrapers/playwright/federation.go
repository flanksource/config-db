package playwright

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

const (
	defaultSessionDuration = 3600
	awsFederationEndpoint  = "https://signin.aws.amazon.com/federation"
)

func getAWSConsoleLoginURL(ctx api.ScrapeContext, login v1.PlaywrightAWSLogin, destination string) (string, error) {
	region := "us-east-1"
	if len(login.Regions) > 0 {
		region = login.Regions[0]
	}

	ctx.Logger.V(2).Infof("generating AWS console login URL for region=%s", region)

	awsConn := login.AWSConnection.ToDutyAWSConnection(region)
	if err := awsConn.Populate(ctx); err != nil {
		return "", fmt.Errorf("populating AWS connection: %w", err)
	}

	cfg, err := awsConn.Client(ctx.Context)
	if err != nil {
		return "", fmt.Errorf("creating AWS client: %w", err)
	}

	ctx.Logger.V(3).Infof("retrieving AWS credentials")
	creds, err := cfg.Credentials.Retrieve(ctx.Context)
	if err != nil {
		return "", fmt.Errorf("retrieving credentials: %w", err)
	}
	ctx.Logger.V(3).Infof("credentials retrieved: accessKeyId=%s...%s, hasSessionToken=%v, expires=%s",
		creds.AccessKeyID[:4], creds.AccessKeyID[len(creds.AccessKeyID)-4:], creds.SessionToken != "", creds.Expires.Format(time.RFC3339))

	if !creds.Expires.IsZero() && creds.Expires.Before(time.Now()) {
		return "", fmt.Errorf("AWS credentials expired at %s (%s ago)", creds.Expires.Format(time.RFC3339), time.Since(creds.Expires).Round(time.Second))
	}

	// Verify credentials are valid by calling STS GetCallerIdentity
	stsClient := sts.NewFromConfig(cfg)
	identity, err := stsClient.GetCallerIdentity(ctx.Context, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("AWS credentials are invalid or expired: %w", err)
	}
	ctx.Logger.V(2).Infof("verified identity: arn=%s, account=%s", aws.ToString(identity.Arn), aws.ToString(identity.Account))

	duration := int32(defaultSessionDuration)
	if login.SessionDuration > 0 {
		duration = int32(login.SessionDuration)
	}

	if creds.SessionToken == "" {
		ctx.Logger.V(2).Infof("no session token, calling STS GetFederationToken (duration=%ds)", duration)
		stsClient := sts.NewFromConfig(cfg)
		out, err := stsClient.GetFederationToken(ctx.Context, &sts.GetFederationTokenInput{
			Name:            aws.String("flanksource-console"),
			DurationSeconds: aws.Int32(duration),
			PolicyArns: []ststypes.PolicyDescriptorType{
				{Arn: aws.String("arn:aws:iam::aws:policy/ReadOnlyAccess")},
			},
		})
		if err != nil {
			return "", fmt.Errorf("getting federation token: %w", err)
		}
		creds.AccessKeyID = *out.Credentials.AccessKeyId
		creds.SecretAccessKey = *out.Credentials.SecretAccessKey
		creds.SessionToken = *out.Credentials.SessionToken
		ctx.Logger.V(2).Infof("federation token obtained, expires=%s", out.Credentials.Expiration.Format(time.RFC3339))
	} else {
		ctx.Logger.V(2).Infof("using existing session token (from AssumeRole or prior session)")
	}

	sessionJSON, _ := json.Marshal(map[string]string{
		"sessionId":    creds.AccessKeyID,
		"sessionKey":   creds.SecretAccessKey,
		"sessionToken": creds.SessionToken,
	})

	signinTokenURL := fmt.Sprintf("%s?Action=getSigninToken&SessionDuration=%d&Session=%s",
		awsFederationEndpoint, duration, url.QueryEscape(string(sessionJSON)))

	ctx.Logger.V(3).Infof("requesting signin token from federation endpoint")
	resp, err := http.Get(signinTokenURL) //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("getting signin token: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading signin token response: %w", err)
	}

	var signinResp struct {
		SigninToken string `json:"SigninToken"`
	}
	if err := json.Unmarshal(body, &signinResp); err != nil {
		return "", fmt.Errorf("parsing signin token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("federation endpoint returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	if signinResp.SigninToken == "" {
		return "", fmt.Errorf("empty signin token (response=%s)", string(body))
	}

	ctx.Logger.V(2).Infof("signin token obtained (length=%d)", len(signinResp.SigninToken))

	issuer := login.Issuer
	if issuer == "" {
		issuer = "https://flanksource.com"
	}

	loginURL := fmt.Sprintf("%s?Action=login&Issuer=%s&Destination=%s&SigninToken=%s",
		awsFederationEndpoint,
		url.QueryEscape(issuer),
		url.QueryEscape(destination),
		url.QueryEscape(signinResp.SigninToken))

	ctx.Logger.V(2).Infof("federation login URL generated for destination=%s", destination)
	return loginURL, nil
}
