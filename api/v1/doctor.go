package v1

import (
	"fmt"
	"strings"

	"github.com/flanksource/clicky"
	clickyapi "github.com/flanksource/clicky/api"
	"github.com/flanksource/clicky/api/icons"
)

type DoctorStatus string

const (
	DoctorStatusPass DoctorStatus = "pass"
	DoctorStatusFail DoctorStatus = "fail"
	DoctorStatusSkip DoctorStatus = "skip"
)

type DoctorResult struct {
	Scraper       string       `json:"scraper"`
	Config        string       `json:"config,omitempty"`
	Resource      string       `json:"resource,omitempty"`
	Operation     string       `json:"operation"`
	Required      []string     `json:"required,omitempty"`
	Granted       []string     `json:"granted,omitempty"`
	GrantEvidence string       `json:"grant_evidence,omitempty"`
	Status        DoctorStatus `json:"status"`
	Message       string       `json:"message,omitempty"`
}

type DoctorResults []DoctorResult

func (results DoctorResults) Failed() bool {
	return results.FailureCount() > 0
}

func (results DoctorResults) FailureCount() int {
	count := 0
	for _, result := range results {
		if result.Status == DoctorStatusFail {
			count++
		}
	}
	return count
}

func (result DoctorResult) Columns() []clickyapi.ColumnDef {
	return []clickyapi.ColumnDef{
		clicky.Column("Status").Build(),
		clicky.Column("Scraper").Build(),
		clicky.Column("Config").Build(),
		clicky.Column("Resource").Build(),
		clicky.Column("Operation").Build(),
		clicky.Column("Required").Build(),
		clicky.Column("Granted").Build(),
		clicky.Column("Evidence").Build(),
		clicky.Column("Message").Build(),
	}
}

func (result DoctorResult) Row() map[string]any {
	return map[string]any{
		"Status":    result.statusText(),
		"Scraper":   result.Scraper,
		"Config":    result.Config,
		"Resource":  result.Resource,
		"Operation": result.Operation,
		"Required":  strings.Join(result.Required, ", "),
		"Granted":   strings.Join(result.Granted, ", "),
		"Evidence":  result.GrantEvidence,
		"Message":   result.Message,
	}
}

func (result DoctorResult) statusText() clickyapi.Textable {
	switch result.Status {
	case DoctorStatusPass:
		return clicky.Text(fmt.Sprintf("%s %s", icons.Success, result.Status), "text-green-600")
	case DoctorStatusFail:
		return clicky.Text(fmt.Sprintf("%s %s", icons.Fail, result.Status), "text-red-600")
	case DoctorStatusSkip:
		return clicky.Text(fmt.Sprintf("%s %s", icons.Skip, result.Status), "text-yellow-600")
	default:
		return clicky.Text(string(result.Status))
	}
}
