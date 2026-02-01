package plugin

import (
	"testing"
	"time"

	pb "github.com/flanksource/config-db/api/plugin/proto"
	v1 v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/models"
)

func TestScrapeResultRoundTrip(t *testing.T) {
	original := v1.ScrapeResult{
		ID:          "test-id-123",
		Name:        "test-resource",
		ConfigClass: "VirtualMachine",
		Type:        "Azure::VirtualMachine",
		Status:      "Running",
		Health:      models.HealthHealthy,
		Tags:        v1.JSONStringMap{"env": "prod", "team": "platform"},
		Labels:      v1.JSONStringMap{"app": "test"},
	}

	proto, err := ScrapeResultToProto(original)
	if err != nil {
		t.Fatalf("ScrapeResultToProto failed: %v", err)
	}

	if proto.Id != original.ID {
		t.Errorf("ID mismatch: got %s, want %s", proto.Id, original.ID)
	}
	if proto.Name != original.Name {
		t.Errorf("Name mismatch: got %s, want %s", proto.Name, original.Name)
	}
	if proto.ConfigClass != original.ConfigClass {
		t.Errorf("ConfigClass mismatch: got %s, want %s", proto.ConfigClass, original.ConfigClass)
	}
	if proto.ConfigType != original.Type {
		t.Errorf("Type mismatch: got %s, want %s", proto.ConfigType, original.Type)
	}

	roundTrip, err := ProtoToScrapeResult(proto)
	if err != nil {
		t.Fatalf("ProtoToScrapeResult failed: %v", err)
	}

	if roundTrip.ID != original.ID {
		t.Errorf("RoundTrip ID mismatch: got %s, want %s", roundTrip.ID, original.ID)
	}
	if roundTrip.Name != original.Name {
		t.Errorf("RoundTrip Name mismatch: got %s, want %s", roundTrip.Name, original.Name)
	}
	if roundTrip.Type != original.Type {
		t.Errorf("RoundTrip Type mismatch: got %s, want %s", roundTrip.Type, original.Type)
	}
}

func TestChangeResultRoundTrip(t *testing.T) {
	now := time.Now()
	createdBy := "user@example.com"
	original := v1.ChangeResult{
		ExternalID:       "change-123",
		ConfigType:       "Azure::VirtualMachine",
		ChangeType:       "CREATE",
		Summary:          "Created VM",
		Severity:         "info",
		Source:           "Azure",
		CreatedAt:        &now,
		CreatedBy:        &createdBy,
		ExternalChangeID: "ext-change-123",
	}

	proto := changeResultToProto(original)

	if proto.ExternalId != original.ExternalID {
		t.Errorf("ExternalID mismatch: got %s, want %s", proto.ExternalId, original.ExternalID)
	}
	if proto.ChangeType != original.ChangeType {
		t.Errorf("ChangeType mismatch: got %s, want %s", proto.ChangeType, original.ChangeType)
	}

	roundTrip := protoToChangeResult(proto)

	if roundTrip.ExternalID != original.ExternalID {
		t.Errorf("RoundTrip ExternalID mismatch: got %s, want %s", roundTrip.ExternalID, original.ExternalID)
	}
	if roundTrip.ChangeType != original.ChangeType {
		t.Errorf("RoundTrip ChangeType mismatch: got %s, want %s", roundTrip.ChangeType, original.ChangeType)
	}
}

func TestAnalysisResultRoundTrip(t *testing.T) {
	original := v1.AnalysisResult{
		Analyzer:     "test-analyzer",
		AnalysisType: models.AnalysisTypeCost,
		Severity:     models.SeverityHigh,
		Summary:      "Test summary",
		Status:       models.AnalysisStatusOpen,
		Source:       "test-source",
	}

	proto := analysisResultToProto(&original)

	if proto.Analyzer != original.Analyzer {
		t.Errorf("Analyzer mismatch: got %s, want %s", proto.Analyzer, original.Analyzer)
	}
	if proto.Summary != original.Summary {
		t.Errorf("Summary mismatch: got %s, want %s", proto.Summary, original.Summary)
	}

	roundTrip := protoToAnalysisResult(proto)

	if roundTrip.Analyzer != original.Analyzer {
		t.Errorf("RoundTrip Analyzer mismatch: got %s, want %s", roundTrip.Analyzer, original.Analyzer)
	}
	if roundTrip.Summary != original.Summary {
		t.Errorf("RoundTrip Summary mismatch: got %s, want %s", roundTrip.Summary, original.Summary)
	}
}

func TestRelationshipResultRoundTrip(t *testing.T) {
	original := v1.RelationshipResult{
		ConfigID: "config-123",
		ConfigExternalID: v1.ExternalID{
			ExternalID: "ext-config-123",
			ConfigType: "Azure::VirtualMachine",
		},
		RelatedExternalID: v1.ExternalID{
			ExternalID: "ext-related-123",
			ConfigType: "Azure::VNet",
		},
		Relationship: "connected",
	}

	proto := relationshipResultToProto(original)

	if proto.ConfigId != original.ConfigID {
		t.Errorf("ConfigID mismatch: got %s, want %s", proto.ConfigId, original.ConfigID)
	}
	if proto.Relationship != original.Relationship {
		t.Errorf("Relationship mismatch: got %s, want %s", proto.Relationship, original.Relationship)
	}

	roundTrip := protoToRelationshipResult(proto)

	if roundTrip.ConfigID != original.ConfigID {
		t.Errorf("RoundTrip ConfigID mismatch: got %s, want %s", roundTrip.ConfigID, original.ConfigID)
	}
	if roundTrip.Relationship != original.Relationship {
		t.Errorf("RoundTrip Relationship mismatch: got %s, want %s", roundTrip.Relationship, original.Relationship)
	}
}

func TestProtoToScrapeResultWithNilInput(t *testing.T) {
	result, err := ProtoToScrapeResult(nil)
	if err != nil {
		t.Errorf("ProtoToScrapeResult should not error on nil: %v", err)
	}
	if result.ID != "" {
		t.Error("Result should be empty for nil input")
	}
}

func TestProtoToChangeResultWithEmptyDetails(t *testing.T) {
	proto := &pb.ChangeResultProto{
		ExternalId: "test",
		ChangeType: "UPDATE",
		Details:    []byte(`{"key": "value"}`),
	}

	result := protoToChangeResult(proto)

	if result.Details == nil {
		t.Error("Details should be unmarshaled")
	}
	if result.Details["key"] != "value" {
		t.Errorf("Details[key] mismatch: got %v, want 'value'", result.Details["key"])
	}
}

func TestProtoToAnalysisResultWithInvalidJSON(t *testing.T) {
	proto := &pb.AnalysisResultProto{
		Analyzer: "test",
		Analysis: []byte(`{invalid json`),
	}

	result := protoToAnalysisResult(proto)

	if result.Analyzer != "test" {
		t.Error("Analyzer should still be set despite invalid JSON")
	}
}
