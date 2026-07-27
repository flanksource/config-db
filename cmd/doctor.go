package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/flanksource/clicky"
	clickyapi "github.com/flanksource/clicky/api"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/duty"
	dutyapi "github.com/flanksource/duty/api"
	dutycontext "github.com/flanksource/duty/context"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

type doctorTargetKind string

const (
	doctorTargetFile doctorTargetKind = "file"
	doctorTargetID   doctorTargetKind = "scraper-id"
)

type doctorTarget struct {
	kind  doctorTargetKind
	value string
	id    uuid.UUID
}

type DoctorOptions struct {
	Targets []string `args:"true" required:"true" help:"scraper.yaml or scraper UUID"`
}

func (DoctorOptions) GetName() string {
	return "doctor <scraper.yaml|scraper-id>"
}

type doctorFailure struct {
	results v1.DoctorResults
	err     error
}

func (failure doctorFailure) Error() string {
	return failure.err.Error()
}

func (failure doctorFailure) Unwrap() error {
	return failure.err
}

func (failure doctorFailure) Pretty() clickyapi.Text {
	return clicky.Text("").Add(clickyapi.NewTableFrom(failure.results))
}

func (failure doctorFailure) MarshalJSON() ([]byte, error) {
	return json.Marshal(failure.results)
}

var Doctor *cobra.Command

func runDoctor(options DoctorOptions) (v1.DoctorResults, error) {
	clicky.Flags.UseFlags()

	if len(options.Targets) != 1 {
		return nil, fmt.Errorf("doctor requires exactly one scraper file or scraper UUID")
	}

	target, err := classifyDoctorTarget(options.Targets[0])
	if err != nil {
		return nil, err
	}

	dutyCtx, err := newDoctorContext(target.kind == doctorTargetID)
	if err != nil {
		return nil, err
	}

	configs, err := loadDoctorConfigs(dutyCtx, target)
	if err != nil {
		return nil, err
	}

	var (
		results   v1.DoctorResults
		doctorErr error
	)
	for index := range configs {
		checks, err := scrapers.RunDoctors(
			api.NewScrapeContext(dutyCtx).WithScrapeConfig(&configs[index]),
		)
		results = append(results, checks...)
		doctorErr = errors.Join(doctorErr, err)
	}

	if doctorErr != nil {
		return nil, doctorFailure{results: results, err: doctorErr}
	}
	if results.Failed() {
		return nil, doctorFailure{
			results: results,
			err:     fmt.Errorf("%d doctor checks failed", results.FailureCount()),
		}
	}
	return results, nil
}

func classifyDoctorTarget(value string) (doctorTarget, error) {
	info, err := os.Stat(value)
	if err == nil {
		if !info.Mode().IsRegular() {
			return doctorTarget{}, fmt.Errorf("doctor target %q is not a regular file", value)
		}
		return doctorTarget{kind: doctorTargetFile, value: value}, nil
	}
	if !os.IsNotExist(err) {
		return doctorTarget{}, fmt.Errorf("inspect doctor target %q: %w", value, err)
	}

	id, parseErr := uuid.Parse(value)
	if parseErr != nil {
		return doctorTarget{}, fmt.Errorf(
			"doctor target %q must be an existing file or scraper UUID",
			value,
		)
	}
	return doctorTarget{kind: doctorTargetID, value: value, id: id}, nil
}

func newDoctorContext(requireDatabase bool) (dutycontext.Context, error) {
	config := dutyapi.DefaultConfig.ReadEnv()
	if config.ConnectionString == "" {
		if requireDatabase {
			return dutycontext.Context{}, fmt.Errorf("scraper-id doctor target requires a configured database")
		}
		return dutycontext.New(), nil
	}

	ctx, _, err := duty.Start(app, duty.ClientOnly)
	if err != nil {
		return dutycontext.Context{}, fmt.Errorf("initialize database: %w", err)
	}
	return ctx, nil
}

func loadDoctorConfigs(ctx dutycontext.Context, target doctorTarget) ([]v1.ScrapeConfig, error) {
	if target.kind == doctorTargetFile {
		configs, err := v1.ParseConfigs(target.value)
		if err != nil {
			return nil, fmt.Errorf("parse doctor config %q: %w", target.value, err)
		}
		return configs, nil
	}

	model, err := db.FindScraper(ctx, target.id.String())
	if err != nil {
		return nil, fmt.Errorf("find scraper %s: %w", target.id, err)
	}
	if model == nil {
		return nil, fmt.Errorf("scraper %s not found", target.id)
	}

	config, err := v1.ScrapeConfigFromModel(*model)
	if err != nil {
		return nil, fmt.Errorf("decode scraper %s: %w", target.id, err)
	}
	return []v1.ScrapeConfig{config}, nil
}

func init() {
	Doctor = clicky.AddCommand(Root, DoctorOptions{}, runDoctor)
	Doctor.Short = "Check scraper configuration and required permissions"
	duty.BindPFlags(Doctor.Flags(), duty.SkipMigrationByDefaultMode)
	clicky.BindAllFlags(Doctor.Flags(), "format")
}
