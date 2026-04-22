package aws

import (
	"encoding/json"
	"fmt"
)

// trustPolicyDoc is a parsed IAM role AssumeRolePolicyDocument.
type trustPolicyDoc struct {
	Version   string           `json:"Version"`
	Statement []trustStatement `json:"Statement"`
}

// trustStatement is a single statement within a trust policy.
type trustStatement struct {
	Sid          string          `json:"Sid,omitempty"`
	Effect       string          `json:"Effect"`
	Principal    principalRef    `json:"Principal,omitempty"`
	NotPrincipal principalRef    `json:"NotPrincipal,omitempty"`
	Action       stringOrSlice   `json:"Action,omitempty"`
	Condition    json.RawMessage `json:"Condition,omitempty"`
}

// principalRef holds the Principal / NotPrincipal block in a trust statement.
// AWS accepts either the literal string "*" or an object keyed by principal type
// (AWS, Service, Federated, CanonicalUser), each value being either a single
// string or a slice of strings.
type principalRef struct {
	Wildcard      bool
	AWS           []string
	Service       []string
	Federated     []string
	CanonicalUser []string
}

func (p principalRef) isEmpty() bool {
	return !p.Wildcard && len(p.AWS) == 0 && len(p.Service) == 0 &&
		len(p.Federated) == 0 && len(p.CanonicalUser) == 0
}

func (p *principalRef) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s == "*" {
			p.Wildcard = true
			return nil
		}
		return fmt.Errorf("unexpected string principal %q", s)
	}
	var obj map[string]stringOrSlice
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("principal is neither string nor object: %w", err)
	}
	for k, v := range obj {
		switch k {
		case "AWS":
			p.AWS = append(p.AWS, v...)
		case "Service":
			p.Service = append(p.Service, v...)
		case "Federated":
			p.Federated = append(p.Federated, v...)
		case "CanonicalUser":
			p.CanonicalUser = append(p.CanonicalUser, v...)
		default:
			return fmt.Errorf("unknown principal key %q", k)
		}
	}
	return nil
}

// stringOrSlice decodes either a JSON string or an array of strings into a slice.
type stringOrSlice []string

func (s *stringOrSlice) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*s = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("value is neither string nor string array: %w", err)
	}
	*s = many
	return nil
}
