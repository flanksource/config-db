package processors

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/gomplate/v3"
	"github.com/magiconair/properties"
	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	kyaml "sigs.k8s.io/yaml"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
)

type Mask struct {
	Selector string // cel expression
	JSONPath *jp.Expr
	Value    string
}

// Filter returns true if the mask selector matches
func (t *Mask) Filter(in v1.ScrapeResult) (bool, error) {
	if t.Selector == "" {
		return false, nil
	}

	res, err := gomplate.RunTemplate(in.AsMap(), gomplate.Template{Expression: t.Selector})
	if err != nil {
		return false, err
	}

	return strconv.ParseBool(res)
}

type Transform struct {
	Script       v1.Script
	Masks        []Mask
	Relationship []v1.RelationshipConfig
}

func (t *Transform) String() string {
	s := ""
	if !t.Script.IsEmpty() {
		s += fmt.Sprintf("script=%s", t.Script)
	}

	for _, m := range t.Masks {
		s += fmt.Sprintf("mask=%s", m)
	}

	s += fmt.Sprintf("relationship=%d", len(t.Relationship))

	return s
}

// ConfigFieldExclusion instructs what fields from the given config types should be removed.
type ConfigFieldExclusion struct {
	jp          jp.Expr
	configTypes []string
}

type Extract struct {
	ID, Type, Class, Name, Description jp.Expr
	Status, Health                     jp.Expr
	CreatedAt, DeletedAt               []jp.Expr
	Items                              *jp.Expr
	Config                             v1.BaseScraper
	Excludes                           []ConfigFieldExclusion
	Transform                          Transform
}

func (e Extract) WithoutItems() Extract {
	return Extract{
		ID:          e.ID,
		Type:        e.Type,
		Name:        e.Name,
		Description: e.Description,
		Status:      e.Status,
		Health:      e.Health,
		Config:      e.Config,
		Excludes:    e.Excludes,
		Transform:   e.Transform,
	}
}

func (e Extract) WithouTransform() Extract {
	return Extract{
		ID:          e.ID,
		Type:        e.Type,
		Name:        e.Name,
		Description: e.Description,
		Status:      e.Status,
		Health:      e.Health,
		Config:      e.Config,
		Excludes:    e.Excludes,
	}
}

func NewExtractor(config v1.BaseScraper) (Extract, error) {
	extract := Extract{
		Config: config,
	}
	if utils.IsJSONPath(config.ID) {
		if x, err := jp.ParseString(config.ID); err != nil {
			return extract, fmt.Errorf("failed to parse id: %s: %v", config.ID, err)
		} else {
			extract.ID = x
		}
	}
	if utils.IsJSONPath(config.Type) {
		if x, err := jp.ParseString(config.Type); err != nil {
			return extract, fmt.Errorf("failed to parse type: %s: %v", config.Type, err)
		} else {
			extract.Type = x
		}
	}
	if utils.IsJSONPath(config.Class) {
		if x, err := jp.ParseString(config.Class); err != nil {
			return extract, fmt.Errorf("failed to parse class: %s: %v", config.Class, err)
		} else {
			extract.Class = x
		}
	}
	if config.Items != "" {
		if x, err := jp.ParseString(config.Items); err != nil {
			return extract, fmt.Errorf("failed to parse items: %s: %v", config.Items, err)
		} else {
			extract.Items = &x
		}
	}

	for _, createdField := range config.CreateFields {
		if utils.IsJSONPath(createdField) {
			if x, err := jp.ParseString(createdField); nil == err {
				extract.CreatedAt = append(extract.CreatedAt, x)
			}
		}
	}

	for _, deletedField := range config.DeleteFields {
		if utils.IsJSONPath(deletedField) {
			if x, err := jp.ParseString(deletedField); nil == err {
				extract.DeletedAt = append(extract.DeletedAt, x)
			}
		}
	}

	if utils.IsJSONPath(config.Name) {
		if x, err := jp.ParseString(config.Name); err != nil {
			return extract, fmt.Errorf("failed to parse name: %s: %v", config.Name, err)
		} else {
			extract.Name = x
		}
	}

	if utils.IsJSONPath(config.Description) {
		if x, err := jp.ParseString(config.Description); err != nil {
			return extract, fmt.Errorf("failed to parse description: %s: %v", config.Description, err)
		} else {
			extract.Description = x
		}
	}

	if utils.IsJSONPath(config.Status) {
		if x, err := jp.ParseString(config.Status); err != nil {
			return extract, fmt.Errorf("failed to parse status: %s: %v", config.Status, err)
		} else {
			extract.Status = x
		}
	}

	if utils.IsJSONPath(config.Health) {
		if x, err := jp.ParseString(config.Health); err != nil {
			return extract, fmt.Errorf("failed to parse health: %s: %v", config.Health, err)
		} else {
			extract.Health = x
		}
	}

	for _, exclude := range config.Transform.Exclude {
		if expr, err := jp.ParseString(exclude.JSONPath); err != nil {
			return extract, fmt.Errorf("failed to parse exclude: %s: %v", exclude.JSONPath, err)
		} else {
			extract.Excludes = append(extract.Excludes, ConfigFieldExclusion{jp: expr, configTypes: exclude.Types})
		}
	}

	extract.Transform.Script = config.Transform.Script
	extract.Transform.Relationship = config.Transform.Relationship

	for _, mask := range config.Transform.Masks {
		if mask.Selector == "" {
			continue
		}

		x, err := jp.ParseString(mask.JSONPath)
		if err != nil {
			return extract, fmt.Errorf("failed to parse mask jsonpath: %s: %v", mask.JSONPath, err)
		}

		extract.Transform.Masks = append(extract.Transform.Masks, Mask{
			Selector: mask.Selector,
			Value:    mask.Value,
			JSONPath: &x,
		})
	}

	return extract, nil
}

func (e Extract) String() string {
	s := ""
	if e.ID != nil {
		s += fmt.Sprintf(" ID: %s", e.ID)
	}
	if e.Type != nil {
		s += fmt.Sprintf(" Type: %s", e.Type)
	}
	if e.Class != nil {
		s += fmt.Sprintf(" Class: %s", e.Class)
	}
	if e.Name != nil {
		s += fmt.Sprintf(" Name: %s", e.Name)
	}
	if e.Description != nil {
		s += fmt.Sprintf(" Description: %s", e.Description)
	}
	if e.Status != nil {
		s += fmt.Sprintf(" Status: %s", e.Status)
	}
	if e.Health != nil {
		s += fmt.Sprintf(" Health: %s", e.Health)
	}

	if e.Items != nil {
		s += fmt.Sprintf(" Items: %s", e.Items)
	}

	s += fmt.Sprintf(" Transform: %s", e.Transform.String())

	return s
}

func getRelationshipsFromRelationshipConfigs(ctx api.ScrapeContext, input v1.ScrapeResult, relationshipConfigs []v1.RelationshipConfig) ([]v1.DirectedRelationship, error) {
	var output []v1.DirectedRelationship

	for _, rc := range relationshipConfigs {
		if rc.Filter != "" {
			filterOutput, err := gomplate.RunTemplate(input.AsMap(), gomplate.Template{Expression: rc.Filter})
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate relationship config filter: %s: %v", rc.Filter, err)
			}

			if ok, err := strconv.ParseBool(filterOutput); err != nil {
				return nil, fmt.Errorf("relationship config filter did not evaulate to a boolean: %s", filterOutput)
			} else if !ok {
				continue
			}
		}

		var relationshipSelectors []v1.DirectedRelationship
		if rc.Expr != "" {
			celOutput, err := gomplate.RunTemplate(input.AsMap(), gomplate.Template{Expression: rc.Expr})
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate relationship config (expr: %s, config_id: %s): %v", rc.Expr, lo.FromPtr(input.ConfigID), err)
			}

			var output []duty.RelationshipSelector
			if err := json.Unmarshal([]byte(celOutput), &output); err != nil {
				return nil, fmt.Errorf("relationship config expr (%s) did not evaulate to a list of relationship selectors: %w", rc.Expr, err)
			}

			for i := range output {
				switch output[i].Scope {
				case "":
					output[i].Scope = string(ctx.ScrapeConfig().GetUID())
				case "all":
					output[i].Scope = ""
				}
			}

			for _, o := range output {
				relationshipSelectors = append(relationshipSelectors, v1.DirectedRelationship{
					Selector: o,
					Parent:   rc.Parent,
				})
			}
		} else {
			if compiled, err := rc.RelationshipSelectorTemplate.Eval(input.Labels, input.AsMap()); err != nil {
				return nil, fmt.Errorf("relationship selector is invalid: %w", err)
			} else if compiled != nil {
				switch compiled.Scope {
				case "":
					compiled.Scope = string(ctx.ScrapeConfig().GetUID())
				case "all":
					compiled.Scope = ""
				}

				relationshipSelectors = append(relationshipSelectors, v1.DirectedRelationship{
					Selector: *compiled,
					Parent:   rc.Parent,
				})
			}
		}

		output = append(output, relationshipSelectors...)
	}

	return output, nil
}

func (e Extract) Extract(ctx api.ScrapeContext, inputs ...v1.ScrapeResult) ([]v1.ScrapeResult, error) {
	var results []v1.ScrapeResult
	var err error

	logScrapes := ctx.PropertyOn(true, "log.items")

	for _, input := range inputs {
		for k, v := range input.BaseScraper.Labels {
			if input.Labels == nil {
				input.Labels = map[string]string{}
			}
			if _, ok := input.Labels[k]; !ok {
				input.Labels[k] = v
			}
		}

		if parsed, err := input.Tags.Eval(input.Labels, input.ConfigString()); err != nil {
			return nil, fmt.Errorf("failed to evaluate tags for result[%s]: %w", input.String(), err)
		} else {
			input.Tags = parsed
		}

		// All tags are stored as labels.
		// This is so that end users do not need to worry about tags/labels as
		// everything will be available as labels when writing cel expressions.
		input.Labels = collections.MergeMap(input.Labels, input.Tags.AsMap())

		if input.Format == "properties" {
			props, err := properties.LoadString(input.Config.(string))
			if err != nil {
				return results, errors.Wrapf(err, "Failed parse properties %s", input)
			}

			propMap := make(map[string]any)
			// Remove comments and tabs
			for key, val := range props.Map() {
				if before, _, exists := strings.Cut(val, "\t"); exists {
					val = before
				}
				if exists := strings.Contains(val, "#"); exists {
					open := false
					for i, ch := range val {
						// Properties with strings are stored in single quotes
						if ch == '\'' {
							open = !open
						}
						if ch == '#' && !open {
							val = strings.TrimSpace(val[0:i])
							break
						}
					}
				}
				propMap[key] = val
			}
			input.Config = propMap
		} else if input.Format == "yaml" {
			contentByte, err := kyaml.YAMLToJSON([]byte(input.Config.(string)))
			if err != nil {
				return results, errors.Wrapf(err, "Failed parse yaml %s", input)
			}
			input.Config = string(contentByte)
		} else if input.Format != "" {
			input.Config = map[string]any{
				"format":  input.Format,
				"content": input.Config,
			}
		}

		if input.Config == nil {
			ctx.Errorf("nothing extracted %s: %v", input, input.Error)
			continue
		}

		var parsedConfig any
		switch v := input.Config.(type) {
		case string:
			parsedConfig, err = oj.ParseString(v)
			if err != nil {
				return results, fmt.Errorf("failed to parse json (format=%s,%s): %v", input.BaseScraper.Format, input.Source, err)
			}
		default:
			opts := oj.Options{OmitNil: input.OmitNil(), Sort: true, UseTags: true, FloatFormat: "%g"}
			err = json.Unmarshal([]byte(oj.JSON(v, &opts)), &parsedConfig)
			if err != nil {
				return results, fmt.Errorf("failed to parse json format=%s,%s): %v", input.Format, input.Source, err)
			}
		}

		if e.Items != nil {
			items := e.Items.Get(parsedConfig)
			ctx.Logger.V(3).Infof("extracted %d items with %s", len(items), *e.Items)
			for _, item := range items {
				extracted, err := e.WithoutItems().Extract(ctx, input.Clone(item))
				if err != nil {
					return results, fmt.Errorf("failed to extract items: %v", err)
				}
				results = append(results, extracted...)
				continue
			}
		}

		input.Config = parsedConfig
		var ongoingInput v1.ScrapeResults = []v1.ScrapeResult{input}
		if !input.BaseScraper.Transform.Script.IsEmpty() {
			ctx.Logger.V(3).Infof("Applying script transformation")
			transformed, err := RunScript(ctx, input, input.BaseScraper.Transform.Script)
			if err != nil {
				return results, fmt.Errorf("failed to run transform script: %v", err)
			}

			ongoingInput = transformed
		}

		for _, result := range ongoingInput {
			for i, configProperty := range result.BaseScraper.Properties {
				if configProperty.Filter != "" {
					if response, err := gomplate.RunTemplate(result.AsMap(), gomplate.Template{Expression: configProperty.Filter}); err != nil {
						result.Errorf("failed to parse filter: %v", err)
						continue
					} else if boolVal, err := strconv.ParseBool(response); err != nil {
						result.Errorf("expected a boolean but property filter returned (%s)", response)
						continue
					} else if !boolVal {
						continue
					}
				}

				// clone the links so as to not mutate the original Links template
				configProperty.Links = make([]types.Link, len(result.BaseScraper.Properties[i].Links))
				copy(configProperty.Links, result.BaseScraper.Properties[i].Links)

				templater := gomplate.StructTemplater{
					Values:         result.AsMap(),
					ValueFunctions: true,
					DelimSets: []gomplate.Delims{
						{Left: "{{", Right: "}}"},
						{Left: "$(", Right: ")"},
					},
				}

				if err := templater.Walk(&configProperty); err != nil {
					result.Errorf("failed to template scraper properties: %v", err)
					continue
				}

				result.Properties = append(result.Properties, &configProperty.Property)
			}

			extracted, err := e.extractAttributes(result)
			if err != nil {
				return results, fmt.Errorf("failed to extract attributes: %v", err)
			}

			if logScrapes {
				ctx.Logger.V(2).Infof("Scraped %s", extracted)
			}

			extracted = extracted.SetHealthIfEmpty()

			// Form new relationships based on the transform configs
			if newRelationships, err := getRelationshipsFromRelationshipConfigs(ctx, extracted, e.Transform.Relationship); err != nil {
				return results, fmt.Errorf("failed to get relationships from relationship configs: %w", err)
			} else if len(newRelationships) > 0 {
				extracted.RelationshipSelectors = append(extracted.RelationshipSelectors, newRelationships...)
			}

			results = append(results, extracted)
		}

		if !input.BaseScraper.Transform.Masks.IsEmpty() {
			results, err = e.applyMask(results)
			if err != nil {
				return results, fmt.Errorf("e.applyMask(); %w", err)
			}
		}
	}

	return results, nil
}

func (e Extract) extractAttributes(input v1.ScrapeResult) (v1.ScrapeResult, error) {
	var err error
	if input.ID == "" {
		input.ID, err = getString(e.ID, input.Config, e.Config.ID)
		if err != nil {
			return input, err
		}
	}

	if input.ID == "" {
		return input, fmt.Errorf("no id defined for: %s: %v", input, e.Config)
	}

	if input.Name == "" {
		input.Name, err = getString(e.Name, input.Config, input.Name)
		if err != nil {
			return input, err
		}
	}

	if input.Description == "" {
		input.Description, err = getString(e.Description, input.Config, input.Description)
		if err != nil {
			return input, err
		}
	}

	if input.Status == "" {
		input.Status, err = getString(e.Status, input.Config, input.Status)
		if err != nil {
			return input, err
		}
	}

	if input.Health == "" {
		h, err := getString(e.Health, input.Config, string(input.Health))
		if err != nil {
			return input, err
		}
		input.Health = models.Health(h)
	}

	if input.BaseScraper.TimestampFormat == "" {
		input.BaseScraper.TimestampFormat = time.RFC3339
	}

	for _, createdAtSelector := range e.CreatedAt {
		createdAt, err := getTimestamp(createdAtSelector, input.Config, input.BaseScraper.TimestampFormat)
		if nil == err {
			input.CreatedAt = &createdAt
			break
		}
	}

	for _, deletedAtSelector := range e.DeletedAt {
		deletedAt, err := getTimestamp(deletedAtSelector, input.Config, input.BaseScraper.TimestampFormat)
		if nil == err {
			input.DeletedAt = &deletedAt
			input.DeleteReason = v1.DeletedReasonFromDeleteField
			break
		}
	}

	if input.Name == "" {
		input.Name = input.ID
	}

	if input.Type == "" {
		input.Type, err = getString(e.Type, input.Config, e.Config.Type)
		if err != nil {
			return input, err
		}
	}

	if input.Type == "" {
		return input, fmt.Errorf("no config type defined for: %s", input)
	}

	if input.ConfigClass == "" {
		defaultClass := e.Config.Class
		if defaultClass == "" {
			defaultClass = input.Type
		}

		input.ConfigClass, err = getString(e.Class, input.Config, defaultClass)
		if err != nil {
			return input, err
		}
	}

	if input.ConfigClass == "" {
		return input, fmt.Errorf("no class defined for: %s", input)
	}

	for _, exclude := range e.Excludes {
		if len(exclude.configTypes) == 0 || collections.MatchItems(input.Type, exclude.configTypes...) {
			if err := exclude.jp.Del(input.Config); err != nil {
				return input, err
			}
		}
	}

	for _, ignore := range input.Ignore {
		if expr, err := jp.ParseString("$." + ignore); err != nil {
			return input, fmt.Errorf("failed to parse  %s: %v", ignore, err)
		} else if err := expr.Del(input.Config); err != nil {
			return input, fmt.Errorf("failed to ignore: %v", err)
		}
	}

	return input, nil
}

func (e Extract) applyMask(results []v1.ScrapeResult) ([]v1.ScrapeResult, error) {
	for _, m := range e.Transform.Masks {
		for i, input := range results {
			if ok, err := m.Filter(input); err != nil || !ok {
				// NOTE: If the cel expression accesses a field that doesn't exist,
				// it will return an error. We treat this errors as a non-match filter.
				continue
			}

			logger.V(4).Infof("Masking %s with %s", input.ID, m.JSONPath)
			identified := m.JSONPath.Get(input.Config)
			for _, y := range identified {
				switch m.Value {
				case "md5sum":
					md5SumHex := md5SumHex(y)
					if err := m.JSONPath.Set(results[i].Config, md5SumHex); err != nil {
						return nil, fmt.Errorf("m.JSONPath.Set(); %w", err)
					}
				default:
					if err := m.JSONPath.Set(results[i].Config, m.Value); err != nil {
						return nil, fmt.Errorf("m.JSONPath.Set(); %w", err)
					}
				}
			}
		}
	}

	return results, nil
}

func getTimestamp(expr jp.Expr, data any, timeFormat string) (time.Time, error) {
	timeStr, err := getString(expr, data, "")
	if err != nil {
		return time.Time{}, err
	}

	parsedTime, err := time.Parse(timeFormat, timeStr)
	if err != nil {
		return time.Time{}, err
	}

	return parsedTime, nil
}

func getString(expr jp.Expr, data any, def string) (string, error) {
	if len(expr) == 0 {
		return def, nil
	}
	o := expr.Get(data)
	if len(o) == 0 {
		logger.Tracef("failed to get %s from:\n %v", expr, data)
		return "", fmt.Errorf("%s not found", expr)
	}
	s := fmt.Sprintf("%v", o[0])
	return s, nil
}

func md5SumHex(i any) string {
	var dataStr string
	switch data := i.(type) {
	case string:
		dataStr = data
	case []byte:
		dataStr = string(data)
	default:
		dataStr = oj.JSON(data, &oj.Options{Sort: true, OmitNil: true, Indent: 2, TimeFormat: "2006-01-02T15:04:05Z07:00"})
	}

	h := md5.Sum([]byte(dataStr))
	return hex.EncodeToString(h[:])
}
