package utils

import (
	"encoding/json"
	"fmt"
	"strings"
)

func IsJSONPath(path string) bool {
	return strings.HasPrefix(path, "$") || strings.HasPrefix(path, "@")
}

func StructToJSON(v any) (string, error) {
	b, err := json.Marshal(&v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ToJSONMap takes an input value of struct or map type and converts it to a map[string]any representation
// using JSON encoding and decoding.
func ToJSONMap(s any) (map[string]any, error) {
	var raw []byte
	var err error

	switch s := s.(type) {
	case string:
		raw = []byte(s)
	case []byte:
		raw = s
	default:
		raw, err = json.Marshal(s)
		if err != nil {
			return nil, err
		}
	}

	result := make(map[string]any)
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// LeafNode represents a leaf node in the JSON tree
type LeafNode struct {
	path   string // path of this node
	parent string // path of its parent
}

// collectLeafNodes recursively traverses the JSON tree and collects all the leaf nodes.
func collectLeafNodes(root map[string]any, parentPath string, leafNodes map[LeafNode]struct{}) {
	for key, value := range root {
		currentPath := fmt.Sprintf("%s.%s", parentPath, key)
		if parentPath == "" {
			currentPath = key
		}

		if child, ok := value.(map[string]any); ok {
			collectLeafNodes(child, currentPath, leafNodes)
		} else {
			n := LeafNode{
				path:   currentPath,
				parent: parentPath,
			}
			leafNodes[n] = struct{}{}
		}
	}
}

// ExtractLeafNodesAndCommonParents takes a JSON map and returns the path of the leaf nodes.
// If multiple nodes with the same parent, then the parent's path is returned.
func ExtractLeafNodesAndCommonParents(data map[string]any) []string {
	leafNodes := make(map[LeafNode]struct{})
	collectLeafNodes(data, "", leafNodes)

	var parents = make(map[string]int)
	for p := range leafNodes {
		parents[p.parent]++
	}

	output := make([]string, 0, len(leafNodes))
	seenPaths := make(map[string]struct{})
	for node := range leafNodes {
		var path string
		if val := parents[node.parent]; val > 1 {
			path = node.parent
		} else {
			path = node.path
		}

		if _, ok := seenPaths[path]; ok {
			continue
		}

		seenPaths[path] = struct{}{}
		output = append(output, path)
	}

	return output
}
