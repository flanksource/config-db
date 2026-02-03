//go:build !slim

package cmd

import (
	"encoding/json"

	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/scrapers/aws"
)

func init() {
	Analyzers = append(Analyzers, aws.EC2InstanceAnalyzer)

	ConfigUnmarshalers["EC2Instance"] = func(obj *v1.ScrapeResult) error {
		nested, _ := json.Marshal(obj.Config)
		instance := aws.Instance{}
		if err := json.Unmarshal(nested, &instance); err != nil {
			return err
		}
		obj.Config = instance
		return nil
	}
}
