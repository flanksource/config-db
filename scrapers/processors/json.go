package processors

import (
	"fmt"
	"strings"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"
)

type Extract struct {
	ID, Type, Name jp.Expr
	Items          *jp.Expr
	Config         v1.BaseScraper
	Excludes       []jp.Expr
}

func (e Extract) WithoutItems() Extract {
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
	if isJSONPath(config.Items) {
		if x, err := jp.ParseString(config.Items); err != nil {
			return extract, fmt.Errorf("failed to parse items: %s: %v", config.Items, err)
		} else {
			extract.Items = &x
		}
	}

	if isJSONPath(config.Name) {
		if x, err := jp.ParseString(config.Name); err != nil {
			return extract, fmt.Errorf("failed to parse items: %s: %v", config.Name, err)
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

	return extract, nil
}

func (e Extract) Extract(inputs ...v1.ScrapeResult) ([]v1.ScrapeResult, error) {
	var results []v1.ScrapeResult
	var err error

	for _, input := range inputs {
		var o interface{}
		if input.Config == nil {
			logger.Errorf("failed to extract %s: %v", input, input.Error)
			continue
		}
		switch input.Config.(type) {
		case string:
			logger.Infof("parsing string")
			o, err = oj.ParseString(input.Config.(string))
			if err != nil {
				return results, fmt.Errorf("failed to parse json: %v", err)
			}
		default:
			opts := oj.Options{OmitNil: true, Sort: true, UseTags: true}
			o, err = oj.ParseString(oj.JSON(input.Config.(interface{}), opts))
			if err != nil {
				return results, fmt.Errorf("failed to parse json: %v", err)
			}
		}

		if e.Items != nil {
			for _, item := range e.Items.Get(o) {
				extracted, err := e.WithoutItems().Extract(input.Clone(item))
				if err != nil {
					return results, fmt.Errorf("failed to extract: %v", err)
				}
				results = append(results, extracted...)
				continue
			}
		}

		for _, exclude := range e.Excludes {
			if err := exclude.Del(o); err != nil {
				return results, fmt.Errorf("failed to exclude: %v", err)
			}
		}

		// opts := oj.Options{Sort: true, OmitNil: true}

		input.Config = o

		if input.ID == "" {
			input.ID, err = getString(e.ID, o, e.Config.ID)
			if err != nil {
				return results, err
			}
		}

		if input.Name == "" {
			input.Name, err = getString(e.Name, o, input.Name)
			if err != nil {
				return results, err
			}
		}

		if input.Name == "" {
			input.Name = input.ID
		}

		if input.Type == "" {
			input.Type, err = getString(e.Type, o, e.Config.Type)
		}
		logger.Infof("Scraped %s", input)
		if err != nil {
			return results, err
		}

		results = append(results, input)
	}
	return results, nil
}

func getString(expr jp.Expr, data interface{}, def string) (string, error) {

	if len(expr) == 0 {
		return def, nil
	}
	o := expr.Get(data)
	if len(o) == 0 {
		return "", fmt.Errorf("%s not found", expr)
	}
	return fmt.Sprintf("%v", o[0]), nil
}

func isJSONPath(path string) bool {
	return strings.HasPrefix(path, "$") || strings.HasPrefix(path, "@")
}
