// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package terraform

import (
	"encoding/json"
	"fmt"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// pathStep is an intermediate representation of a cty.pathStep to facilitate
// consistent JSON serialization. The Value field can either be a cty.Value of
// dynamic type (for index steps), or a string (for get attr steps).
type pathStep struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

const (
	indexPathStepType   = "index"
	getAttrPathStepType = "get_attr"
)

func unmarshalPaths(buf []byte) ([]cty.Path, error) {
	var jsonPaths [][]pathStep

	err := json.Unmarshal(buf, &jsonPaths)
	if err != nil {
		return nil, err
	}

	if len(jsonPaths) == 0 {
		return nil, nil
	}
	paths := make([]cty.Path, 0, len(jsonPaths))

	for _, jsonPath := range jsonPaths {
		var path cty.Path
		for _, jsonStep := range jsonPath {
			switch jsonStep.Type {
			case indexPathStepType:
				key, err := ctyjson.Unmarshal(jsonStep.Value, cty.DynamicPseudoType)
				if err != nil {
					return nil, fmt.Errorf("failed to unmarshal index step key: %w", err)
				}
				path = append(path, cty.IndexStep{Key: key})
			case getAttrPathStepType:
				var name string
				if err := json.Unmarshal(jsonStep.Value, &name); err != nil {
					return nil, fmt.Errorf("failed to unmarshal get attr step name: %w", err)
				}
				path = append(path, cty.GetAttrStep{Name: name})
			default:
				return nil, fmt.Errorf("unsupported path step type %q", jsonStep.Type)
			}
		}
		paths = append(paths, path)
	}

	return paths, nil
}
