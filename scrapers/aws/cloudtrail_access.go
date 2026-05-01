package aws

import (
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/flanksource/commons/hash"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/samber/lo"
)

// assumeRoleEventNames is the set of STS events treated as role-access signals.
// AssumeRoleWithSAML and AssumeRoleWithWebIdentity cover SAML IdPs, GitHub
// Actions OIDC, and EKS IRSA — the current code only matches "AssumeRole"
// and silently drops the other two.
var assumeRoleEventNames = map[string]struct{}{
	"AssumeRole":                 {},
	"AssumeRoleWithSAML":         {},
	"AssumeRoleWithWebIdentity":  {},
}

func isAssumeRoleEvent(eventName string) bool {
	_, ok := assumeRoleEventNames[eventName]
	return ok
}

// callerIdentity is the minimal view of who made the API call, lifted out
// of a CloudTrail event. Both the role-level access log and downstream
// resource access logs share this extractor.
type callerIdentity struct {
	User dutyModels.ExternalUser
	MFA  bool
}

// extractCaller turns a parsed CloudTrailEvent.UserIdentity into an
// ExternalUser (with deterministic ID) plus MFA flag. Returns (nil,false,nil)
// for AWSService callers — those are not tracked as access-log principals.
func extractCaller(ctEvent CloudTrailEvent) (*callerIdentity, error) {
	userType := ctEvent.UserIdentity.Type
	if userType == "AWSService" {
		return nil, nil
	}

	var userName, userARN, accountID string
	switch userType {
	case "IAMUser":
		userName = ctEvent.UserIdentity.Username
		userARN = ctEvent.UserIdentity.Arn
		accountID = ctEvent.UserIdentity.AccountID
	case "AssumedRole":
		userName = ctEvent.UserIdentity.SessionContext.SessionIssuer.Username
		userARN = ctEvent.UserIdentity.SessionContext.SessionIssuer.Arn
		accountID = ctEvent.UserIdentity.AccountID
		if userARN == "" {
			userARN = ctEvent.UserIdentity.Arn
		}
		if userName == "" {
			userName = userARN
		}
	default:
		userName = ctEvent.UserIdentity.Arn
		userARN = ctEvent.UserIdentity.Arn
		accountID = ctEvent.UserIdentity.AccountID
	}

	if userARN == "" {
		return nil, fmt.Errorf("event has no caller ARN")
	}

	aliases := pq.StringArray{userARN}
	userID, err := hash.DeterministicUUID(aliases)
	if err != nil {
		return nil, fmt.Errorf("error generating user id: %w", err)
	}

	return &callerIdentity{
		User: dutyModels.ExternalUser{
			ID:       userID,
			Name:     userName,
			Aliases:  aliases,
			Tenant:   accountID,
			UserType: userType,
		},
		MFA: ctEvent.UserIdentity.SessionContext.Attributes.MfaAuthenticated == "true",
	}, nil
}

// assumeRoleTargetARN returns the role ARN that an AssumeRole* event
// targeted, preferring requestParameters.roleArn then resources[].ARN.
func assumeRoleTargetARN(ctEvent CloudTrailEvent) string {
	if ctEvent.RequestParameters.RoleArn != "" {
		return ctEvent.RequestParameters.RoleArn
	}
	for _, r := range ctEvent.Resources {
		if r.ARN != "" {
			return r.ARN
		}
	}
	return ""
}

// accessLogKey identifies a per-day aggregated access log row.
type accessLogKey struct {
	configType string
	configARN  string
	userID     uuid.UUID
	day        time.Time
}

// accessLogAggregator collapses multiple access events per (config, user, day)
// into a single ConfigAccessLog row with Count and OR-accumulated MFA.
// config_access_logs is keyed by (config_id, external_user_id, scraper_id),
// so per-day bucketing matches the upsert semantics — without it, repeated
// events clobber prior rows instead of accumulating.
type accessLogAggregator struct {
	entries map[accessLogKey]*aggregatedEntry
}

type aggregatedEntry struct {
	user     dutyModels.ExternalUser
	log      v1.ExternalConfigAccessLog
	count    int
	mfa      bool
	latestAt time.Time
}

func newAccessLogAggregator() *accessLogAggregator {
	return &accessLogAggregator{entries: map[accessLogKey]*aggregatedEntry{}}
}

// addAssumeRole records one AssumeRole* event.
func (a *accessLogAggregator) addAssumeRole(event types.Event, ctEvent CloudTrailEvent) error {
	roleARN := assumeRoleTargetARN(ctEvent)
	if roleARN == "" {
		return fmt.Errorf("AssumeRole event has no role ARN")
	}
	caller, err := extractCaller(ctEvent)
	if err != nil {
		return err
	}
	if caller == nil {
		return nil
	}
	return a.add(addParams{
		configType: v1.AWSIAMRole,
		configARN:  roleARN,
		caller:     *caller,
		eventTime:  lo.FromPtrOr(event.EventTime, time.Time{}),
	})
}

type addParams struct {
	configType string
	configARN  string
	caller     callerIdentity
	eventTime  time.Time
}

func (a *accessLogAggregator) add(p addParams) error {
	day := p.eventTime.UTC().Truncate(24 * time.Hour)
	key := accessLogKey{
		configType: p.configType,
		configARN:  p.configARN,
		userID:     p.caller.User.ID,
		day:        day,
	}
	entry, ok := a.entries[key]
	if !ok {
		entry = &aggregatedEntry{
			user: p.caller.User,
			log: v1.ExternalConfigAccessLog{
				ConfigAccessLog: dutyModels.ConfigAccessLog{
					ExternalUserID: p.caller.User.ID,
				},
				ConfigExternalID: v1.ExternalID{
					ConfigType: p.configType,
					ExternalID: p.configARN,
				},
			},
		}
		a.entries[key] = entry
	}
	entry.count++
	if p.caller.MFA {
		entry.mfa = true
	}
	if p.eventTime.After(entry.latestAt) {
		entry.latestAt = p.eventTime
	}
	return nil
}

// flush materializes the aggregated entries into a single ScrapeResult
// carrying every unique ExternalUser and one ConfigAccessLog per bucket.
func (a *accessLogAggregator) flush() v1.ScrapeResult {
	var sr v1.ScrapeResult
	seenUser := map[uuid.UUID]bool{}
	for _, e := range a.entries {
		if !seenUser[e.user.ID] {
			seenUser[e.user.ID] = true
			sr.ExternalUsers = append(sr.ExternalUsers, e.user)
		}
		count := e.count
		log := e.log
		log.Count = &count
		log.MFA = e.mfa
		log.CreatedAt = e.latestAt
		sr.ConfigAccessLogs = append(sr.ConfigAccessLogs, log)
	}
	return sr
}

// isEmpty reports whether the aggregator collected anything worth flushing.
func (a *accessLogAggregator) isEmpty() bool {
	return len(a.entries) == 0
}
