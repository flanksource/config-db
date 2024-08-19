package terraform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/zclconf/go-cty/cty"
	gocty "github.com/zclconf/go-cty/cty/gocty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

func attributeToCtyValue(attributes map[string]any) (cty.Value, error) {
	attributeJSON, err := json.Marshal(attributes)
	if err != nil {
		return cty.Value{}, err
	}

	impliedType, err := ctyjson.ImpliedType(attributeJSON)
	if err != nil {
		return cty.Value{}, fmt.Errorf("error unmarshaling state to gocty value: %w", err)
	}

	return gocty.ToCtyValue(attributes, impliedType)
}

func maskSensitiveAttributes(state State, data []byte) (map[string]any, error) {
	var stateFileRaw map[string]any
	if err := json.Unmarshal(data, &stateFileRaw); err != nil {
		return nil, err
	}

	resources := stateFileRaw["resources"].([]any)
	for i, resource := range state.Resources {
		rawResource := resources[i]
		rawInstances := rawResource.(map[string]any)["instances"].([]any)
		for j, instance := range resource.Instances {
			ctyValue, err := attributeToCtyValue(instance.Attributes)
			if err != nil {
				return nil, err
			}

			sensitivePaths, err := unmarshalPaths(instance.SensitiveAttributes)
			if err != nil {
				return nil, err
			}

			// Transform the ctyValue, masking sensitive attributes
			maskedValue, err := cty.Transform(ctyValue, func(path cty.Path, v cty.Value) (cty.Value, error) {
				for _, sensitivePath := range sensitivePaths {
					if path.Equals(sensitivePath) {
						var strToHash string
						switch v.Type() {
						case cty.Number:
							bf := v.AsBigFloat()
							strToHash = bf.Text('f', -1)
						case cty.String:
							strToHash = v.AsString()
						default:
							// For complex types or unsupported types, use cty's string representation
							strToHash = v.GoString()
						}

						hash := sha256.Sum256([]byte(strToHash))
						hashString := hex.EncodeToString(hash[:])
						return cty.StringVal(fmt.Sprintf("sha256(%s)", hashString)), nil
					}
				}
				return v, nil
			})
			if err != nil {
				return nil, fmt.Errorf("error masking values: %w", err)
			}

			maskedJSON, err := ctyjson.Marshal(maskedValue, maskedValue.Type())
			if err != nil {
				return nil, fmt.Errorf("error marshaling masked value to JSON: %w", err)
			}

			var maskedAttribute map[string]any
			if err := json.Unmarshal(maskedJSON, &maskedAttribute); err != nil {
				return nil, fmt.Errorf("error unmarshaling masked JSON to instance: %w", err)
			}

			rawInstances[j].(map[string]any)["attributes"] = maskedAttribute
		}
	}

	return stateFileRaw, nil
}
