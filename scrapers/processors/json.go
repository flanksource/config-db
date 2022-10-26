package processors

import (
	"fmt"
	"strings"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/magiconair/properties"
	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"
	"github.com/pkg/errors"
)

type Extract struct {
	ID, Type, Name jp.Expr
	Items          *jp.Expr
	Config         v1.BaseScraper
	Excludes       []jp.Expr
	Transform      v1.Script
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
	if config.Items != "" {
		if x, err := jp.ParseString(config.Items); err != nil {
			return extract, fmt.Errorf("failed to parse items: %s: %v", config.Items, err)
		} else {
			extract.Items = &x
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
		if x, err := jp.ParseString(exclude.JSONPath); err != nil {
			return extract, fmt.Errorf("failed to parse exclude: %s: %v", exclude.JSONPath, err)
		} else {
			extract.Excludes = append(extract.Excludes, x)
		}
	}

	extract.Transform = config.Transform.Script
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
	if e.Name != nil {
		s += fmt.Sprintf(" Name: %s", e.Name)
	}
	if e.Items != nil {
		s += fmt.Sprintf(" Items: %s", e.Items)
	}

	s += fmt.Sprintf(" Transform: %s", e.Transform)

	return s
}

func (e Extract) Extract(inputs ...v1.ScrapeResult) ([]v1.ScrapeResult, error) {
	var results []v1.ScrapeResult
	var err error

	for _, input := range inputs {
		var o interface{}
		if input.Format == "properties" {
			props, err := properties.LoadString(input.Config.(string))
			if err != nil {
				return results, errors.Wrapf(err, "Failed parse properties %s", input)
			}
			input.Config = props.Map()
		}

		if input.Config == nil {
			logger.Errorf("nothing extracted %s: %v", input, input.Error)
			continue
		}

		switch input.Config.(type) {
		case string:
			o, err = oj.ParseString(input.Config.(string))
			if err != nil {
				return results, fmt.Errorf("failed to parse json: %v", err)
			}
		default:
			opts := oj.Options{OmitNil: true, Sort: true, UseTags: true}
			o, err = oj.ParseString(oj.JSON(input.Config, opts))
			if err != nil {
				return results, fmt.Errorf("failed to parse json: %v", err)
			}
		}

		if e.Items != nil {
			items := e.Items.Get(o)
			logger.Debugf("Exctracted %d items with %s", len(items), *e.Items)
			for _, item := range items {
				extracted, err := e.WithoutItems().Extract(input.Clone(item))
				if err != nil {
					return results, fmt.Errorf("failed to extract items: %v", err)
				}
				results = append(results, extracted...)
				continue
			}
		}

		input.Config = o

		if !input.BaseScraper.Transform.Script.IsEmpty() {
			transformed, err := RunScript(input, input.BaseScraper.Transform.Script)
			if err != nil {
				return results, fmt.Errorf("failed to run script: %v", err)
			}
			for _, result := range transformed {
				if extracted, err := e.extractAttributes(result); err != nil {
					return results, fmt.Errorf("failed to extract attributes: %v", err)
				} else {
					logger.Debugf("Scraped %s", extracted)
					results = append(results, extracted)
				}
			}
			continue
		}

		if input, err := e.extractAttributes(input); err != nil {
			return nil, fmt.Errorf("failed to extract attributes: %v", err)
		} else {
			logger.Debugf("Scraped %s", input)
			results = append(results, input)
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

	if input.Name == "" {
		input.Name, err = getString(e.Name, input.Config, input.Name)
		if err != nil {
			return input, err
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

	for _, exclude := range e.Excludes {
		if err := exclude.Del(input.Config); err != nil {
			return input, err
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

func getString(expr jp.Expr, data interface{}, def string) (string, error) {
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
