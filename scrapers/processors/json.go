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
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/gomplate/v3"
	"github.com/magiconair/properties"
	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"
	"github.com/pkg/errors"
	kyaml "sigs.k8s.io/yaml"
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

// ConfigFieldExclusion instructs what fields from the given config types should be removed.
type ConfigFieldExclusion struct {
	jp          jp.Expr
	configTypes []string
}

type Extract struct {
	ID, Type, Class, Name jp.Expr
	CreatedAt, DeletedAt  []jp.Expr
	Items                 *jp.Expr
	Config                v1.BaseScraper
	Excludes              []ConfigFieldExclusion
	Transform             Transform
}

func (e Extract) WithoutItems() Extract {
	return Extract{
		ID:        e.ID,
		Type:      e.Type,
		Name:      e.Name,
		Config:    e.Config,
		Excludes:  e.Excludes,
		Transform: e.Transform,
	}
}

func (e Extract) WithouTransform() Extract {
	return Extract{
		ID:       e.ID,
		Type:     e.Type,
		Name:     e.Name,
		Config:   e.Config,
		Excludes: e.Excludes,
	}
}

func NewExtractor(config v1.BaseScraper) (Extract, error) {
	extract := Extract{
		Config: config,
	}
	if isJSONPath(config.ID) {
		if x, err := jp.ParseString(config.ID); err != nil {
			return extract, fmt.Errorf("failed to parse id: %s: %v", config.ID, err)
		} else {
			extract.ID = x
		}
	}
	if isJSONPath(config.Type) {
		if x, err := jp.ParseString(config.Type); err != nil {
			return extract, fmt.Errorf("failed to parse type: %s: %v", config.Type, err)
		} else {
			extract.Type = x
		}
	}
	if isJSONPath(config.Class) {
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
		if isJSONPath(createdField) {
			if x, err := jp.ParseString(createdField); nil == err {
				extract.CreatedAt = append(extract.CreatedAt, x)
			}
		}
	}

	for _, deletedField := range config.DeleteFields {
		if isJSONPath(deletedField) {
			if x, err := jp.ParseString(deletedField); nil == err {
				extract.DeletedAt = append(extract.DeletedAt, x)
			}
		}
	}

	if isJSONPath(config.Name) {
		if x, err := jp.ParseString(config.Name); err != nil {
			return extract, fmt.Errorf("failed to parse name: %s: %v", config.Name, err)
		} else {
			extract.Name = x
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
	if e.Items != nil {
		s += fmt.Sprintf(" Items: %s", e.Items)
	}

	s += fmt.Sprintf(" Transform: %s", e.Transform)

	return s
}

func getRelationshipsFromRelationshipConfigs(ctx context.Context, input v1.ScrapeResult, relationshipConfigs []v1.RelationshipConfig) ([]v1.RelationshipSelector, error) {
	var output []v1.RelationshipSelector

	for _, rc := range relationshipConfigs {
		if rc.Filter != "" {
			filterOutput, err := gomplate.RunTemplate(input.AsMap(), gomplate.Template{Expression: rc.Filter})
			if err != nil {
				return nil, err
			}

			if ok, err := strconv.ParseBool(filterOutput); err != nil {
				return nil, err
			} else if !ok {
				continue
			}
		}

		var relationshipSelectors []v1.RelationshipSelector
		if rc.Expr != "" {
			celOutput, err := gomplate.RunTemplate(input.AsMap(), gomplate.Template{Expression: rc.Expr})
			if err != nil {
				return nil, err
			}

			var output []v1.RelationshipSelector
			if err := json.Unmarshal([]byte(celOutput), &output); err != nil {
				return nil, err
			}
			relationshipSelectors = append(relationshipSelectors, output...)
		} else {
			if compiled, err := rc.RelationshipSelectorTemplate.Eval(input.Tags, input.AsMap()); err != nil {
				return nil, err
			} else if compiled != nil {
				relationshipSelectors = append(relationshipSelectors, *compiled)
			}
		}

		output = append(output, relationshipSelectors...)
	}

	return output, nil
}

func (e Extract) Extract(ctx context.Context, inputs ...v1.ScrapeResult) ([]v1.ScrapeResult, error) {
	var results []v1.ScrapeResult
	var err error

	for _, input := range inputs {
		for k, v := range input.BaseScraper.Tags {
			if input.Tags == nil {
				input.Tags = map[string]string{}
			}
			if _, ok := input.Tags[k]; !ok {
				input.Tags[k] = v
			}
		}

		// Form new relationships based on the transform configs
		if newRelationships, err := getRelationshipsFromRelationshipConfigs(ctx, input, e.Transform.Relationship); err != nil {
			return results, err
		} else if len(newRelationships) > 0 {
			input.RelationshipSelectors = append(input.RelationshipSelectors, newRelationships...)
		}

		for i, configProperty := range input.BaseScraper.Properties {
			if configProperty.Filter != "" {
				if response, err := gomplate.RunTemplate(input.AsMap(), gomplate.Template{Expression: configProperty.Filter}); err != nil {
					input.Errorf("failed to parse filter: %v", err)
					continue
				} else if boolVal, err := strconv.ParseBool(response); err != nil {
					input.Errorf("expected a boolean but property filter returned (%s)", response)
					continue
				} else if !boolVal {
					continue
				}
			}

			// clone the links so as to not mutate the original Links template
			configProperty.Links = make([]types.Link, len(input.BaseScraper.Properties[i].Links))
			copy(configProperty.Links, input.BaseScraper.Properties[i].Links)

			templater := gomplate.StructTemplater{
				Values:         input.AsMap(),
				ValueFunctions: true,
				DelimSets: []gomplate.Delims{
					{Left: "{{", Right: "}}"},
					{Left: "$(", Right: ")"},
				},
			}

			if err := templater.Walk(configProperty); err != nil {
				input.Errorf("failed to template scraper properties: %v", err)
				continue
			}

			input.Properties = append(input.Properties, &configProperty.Property)
		}

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
			logger.Errorf("nothing extracted %s: %v", input, input.Error)
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
			opts := oj.Options{OmitNil: true, Sort: true, UseTags: true}
			parsedConfig, err = oj.ParseString(oj.JSON(v, &opts))
			if err != nil {
				return results, fmt.Errorf("failed to parse json format=%s,%s): %v", input.Format, input.Source, err)
			}
		}

		if e.Items != nil {
			items := e.Items.Get(parsedConfig)
			logger.Debugf("extracted %d items with %s", len(items), *e.Items)
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
			logger.Debugf("Applying script transformation")
			transformed, err := RunScript(input, input.BaseScraper.Transform.Script)
			if err != nil {
				return results, fmt.Errorf("failed to run script: %v", err)
			}

			ongoingInput = transformed
		}

		for _, result := range ongoingInput {
			if extracted, err := e.extractAttributes(result); err != nil {
				return results, fmt.Errorf("failed to extract attributes: %v", err)
			} else {
				logger.Debugf("Scraped %s", extracted)
				results = append(results, extracted)
			}
		}

		if !input.BaseScraper.Transform.Masks.IsEmpty() {
			logger.Debugf("Applying mask transformation")
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

func isJSONPath(path string) bool {
	return strings.HasPrefix(path, "$") || strings.HasPrefix(path, "@")
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
