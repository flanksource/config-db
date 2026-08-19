package scrapers

import (
	"testing"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEffectiveRunLogLevel(t *testing.T) {
	tests := []struct {
		name        string
		process     logger.LogLevel
		spec        string
		annotations map[string]string
		want        logger.LogLevel
	}{
		{name: "process level without override", process: logger.Debug, want: logger.Debug},
		{name: "spec increases verbosity", process: logger.Info, spec: "debug", want: logger.Debug},
		{name: "process remains more verbose", process: logger.Trace, spec: "info", want: logger.Trace},
		{name: "spec remains more verbose", process: logger.Debug, spec: "trace", want: logger.Trace},
		{name: "debug annotation increases verbosity", process: logger.Info, annotations: map[string]string{"debug": "true"}, want: logger.Debug},
		{name: "trace annotation remains more verbose", process: logger.Debug, spec: "info", annotations: map[string]string{"trace": "true"}, want: logger.Trace},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &v1.ScrapeConfig{
				ObjectMeta: metav1.ObjectMeta{Annotations: tt.annotations},
				Spec:       v1.ScraperSpec{LogLevel: tt.spec},
			}
			if got := effectiveRunLogLevel(tt.process, config); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}
