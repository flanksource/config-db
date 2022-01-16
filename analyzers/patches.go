package analyzers

import (
	"fmt"
	"sort"
	"strings"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
)

func PatchAnalyzer(configs []v1.ScrapeResult) v1.AnalysisResult {

	result := v1.AnalysisResult{
		Analyzer: "patch",
	}

	var platforms = make(map[string][]v1.Host)
	var hostIndex = make(map[string]v1.Host)
	for _, config := range configs {
		switch config.Config.(type) {
		case v1.Host:
			host := config.Config.(v1.Host)
			hostIndex[host.GetId()] = host
			platforms[host.GetPlatform()] = append(platforms[host.GetPlatform()], host)
		}
	}

	for platform, hosts := range platforms {
		if len(hosts) == 1 {
			logger.Infof("[%s] skipping analysis on a single host", platform)
			continue
		}
		logger.Infof("[%s] %d hosts", platform, len(hosts))
		hostPatches := make(map[string]map[string]string)
		allPatches := make(map[string]bool)
		for _, host := range hosts {
			for _, patch := range host.GetPatches() {
				if _, ok := hostPatches[host.GetId()]; !ok {
					hostPatches[host.GetId()] = make(map[string]string)
				}
				hostPatches[host.GetId()][patch.GetTitle()] = patch.GetVersion()
				allPatches[patch.GetTitle()] = true
			}
		}

		logger.Infof("Unique Patches: %d, Hosts with patches: %d, Hosts without: %d", len(allPatches), len(hostPatches), len(hosts)-len(hostPatches))
		for _, patches := range hostPatches {
			for patch := range allPatches {
				if _, found := patches[patch]; !found {
					allPatches[patch] = false
				}
			}
		}

		commonPatches := []string{}
		appliedByHost := map[string][]string{}
		notAppliedByHost := map[string][]string{}
		for patch, common := range allPatches {
			if common {
				commonPatches = append(commonPatches, patch)
				continue
			}
			appliedHosts := []string{}
			for host, patches := range hostPatches {
				if _, ok := patches[patch]; ok {
					appliedHosts = append(appliedHosts, hostIndex[host].GetHostname())
				}
			}

			notApplied := []string{}
			for _, host := range hosts {
				if !inSlice(host.GetHostname(), appliedHosts) {
					notApplied = append(notApplied, host.GetHostname())
				}
			}

			if len(notApplied) == 1 {
				notAppliedByHost[notApplied[0]] = append(notAppliedByHost[notApplied[0]], patch)
			} else if len(appliedHosts) == 1 {
				appliedByHost[appliedHosts[0]] = append(appliedByHost[appliedHosts[0]], patch)
			} else if len(notApplied) > len(appliedHosts) {
				result.Messages = append(result.Messages, fmt.Sprintf("%s is only applied to %s", patch, strings.Join(appliedHosts, ",")))
			} else {
				result.Messages = append(result.Messages, fmt.Sprintf("%s is not applied to %s", patch, strings.Join(notApplied, ",")))
			}
		}
		for host, patches := range notAppliedByHost {
			sort.Strings(patches)
			result.Messages = append(result.Messages, fmt.Sprintf("%s has not applied\n\t%s", host, strings.Join(patches, "\n\t")))
		}
		for host, patches := range appliedByHost {
			sort.Strings(patches)
			result.Messages = append(result.Messages, fmt.Sprintf("%s has only applied \n\t%s", host, strings.Join(patches, "\n\t")))
		}
	}
	return result
}

func inSlice(v string, in []string) bool {
	for _, i := range in {
		if i == v {
			return true
		}
	}
	return false
}
