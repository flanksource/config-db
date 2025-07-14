package gcp

import (
	"maps"
	"slices"
	"strings"

	"github.com/flanksource/commons/logger"
	"github.com/samber/lo/mutable"
	"google.golang.org/protobuf/types/known/structpb"

	v1 "github.com/flanksource/config-db/api/v1"
)

func mergeDNSRecordSetsIntoManagedZone(results v1.ScrapeResults) v1.ScrapeResults {
	managedZones := v1.ScrapeResults{}
	dnsRecordSets := v1.ScrapeResults{}
	var allResults v1.ScrapeResults
	mzNameToDNSName := make(map[string]string)
	for _, r := range results {
		switch r.Type {
		case v1.GCPManagedZone:
			managedZones = append(managedZones, r)
			dnsName := r.GCPStructPB.Fields["dnsName"].GetStringValue()
			mzNameToDNSName[dnsName] = r.Name
		case v1.GCPResourceRecordSet:
			dnsRecordSets = append(dnsRecordSets, r)
		default:
			allResults = append(allResults, r)
		}
	}

	mzNames := slices.Collect(maps.Keys(mzNameToDNSName))
	slices.Sort(mzNames)
	mutable.Reverse(mzNames)

	getScrapeResultForMZ := func(dnsName string) *v1.ScrapeResult {
		name := mzNameToDNSName[dnsName]
		for _, mz := range managedZones {
			if mz.Name == name {
				return &mz
			}
		}
		return nil
	}

	mzRecordSetMap := make(map[string][]*structpb.Struct)

	for _, d := range dnsRecordSets {
		record := getFieldValue(d.GCPStructPB, []string{"name"})
		for _, mz := range mzNames {
			if strings.HasSuffix(record, mz) {
				mzRecordSetMap[mz] = append(mzRecordSetMap[mz], d.GCPStructPB)
			}
		}
	}

	for mz, recordSets := range mzRecordSetMap {
		if mzScrapeResult := getScrapeResultForMZ(mz); mzScrapeResult != nil {
			if err := AddStructArrayField(mzScrapeResult.GCPStructPB, recordSets, "recordSets"); err == nil {
				mzScrapeResult.Config = mzScrapeResult.GCPStructPB
				logger.Infof("Updated config for mz %s", mzScrapeResult.Name)
			} else {
				logger.V(1).Infof("error merging structs: %v", err)
			}
		}

	}
	return append(allResults, managedZones...)
}
