package models

import (
	"fmt"
	"strings"
	"time"

	v1 "github.com/flanksource/confighub/api/v1"
)

type Analysis struct {
	ExternalID    string           `gorm:"-"`
	ExternalType  string           `gorm:"-"`
	ID            string           `gorm:"primaryKey;unique_index;not null;column:id" json:"id"`
	ConfigID      string           `gorm:"column:config_id;default:''" json:"config_id"`
	Analyzer      string           `gorm:"column:analyzer" json:"analyzer"`
	Message       string           `gorm:"column:message" json:"message"`
	Summary       string           `gorm:"column:summary;default:null" json:"summary,omitempty"`
	Status        string           `gorm:"column:status;default:null" json:"status,omitempty"`
	Severity      string           `gorm:"column:severity" json:"severity"`
	AnalysisType  string           `gorm:"column:analysis_type" json:"change_type"`
	Analysis      v1.JSONStringMap `gorm:"column:analysis" json:"analysis,omitempty"`
	FirstObserved *time.Time       `gorm:"column:first_observed;<-:false" json:"first_observed"`
	LastObserved  *time.Time       `gorm:"column:last_observed" json:"last_observed"`
}

func (a Analysis) TableName() string {
	return "config_analysis"
}

func (a Analysis) String() string {
	return fmt.Sprintf("[%s/%s] %s", a.ExternalType, a.ExternalID, a.Analyzer)
}

func NewAnalysisFromV1(analysis v1.AnalysisResult) Analysis {
	// tags := v1.JSONStringMap(analysis.Analysis)
	return Analysis{
		ExternalID:   analysis.ExternalID,
		ExternalType: analysis.ExternalType,
		// Analysis:      &tags,
		Analyzer:      analysis.Analyzer,
		Message:       strings.Join(analysis.Messages, ";"),
		Severity:      analysis.Severity,
		AnalysisType:  analysis.AnalysisType,
		Summary:       analysis.Summary,
		Status:        analysis.Status,
		FirstObserved: analysis.FirstObserved,
		LastObserved:  analysis.LastObserved,
	}
}
