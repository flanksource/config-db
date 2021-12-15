package aws

import (
	"time"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
)

func EC2InstanceAnalyzer(configs []v1.ScrapeResult) v1.AnalysisResult {

	result := v1.AnalysisResult{}
	for _, config := range configs {
		switch config.Config.(type) {
		case Instance:
			host := config.Config.(Instance)
			logger.Infof("[%s/%s] os=%s %s, failed=%d missing=%d installed=%d", host.GetHostname(), host.GetId(), host.GetPlatform(), time.Since(*host.PatchState.OperationEndTime), host.PatchState.FailedCount, host.PatchState.MissingCount, host.PatchState.InstalledCount)

			for name, compliance := range host.Compliance {
				if compliance.ComplianceType != "COMPLIANT" {
					logger.Infof("[%s/%s] %s - %s: %s", host.GetHostname(), host.GetId(), name, compliance.ComplianceType, compliance.Annotation)
				}
			}
		}
	}

	return result
}
