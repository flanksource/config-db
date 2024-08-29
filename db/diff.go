package db

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/flanksource/commons/properties"
	dutyContext "github.com/flanksource/duty/context"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/ohler55/ojg"
	"github.com/ohler55/ojg/oj"
)

// We expose this function to replace it with a rust function called via FFI
// when the build tag rustdiffgen is provided
var DiffFunc func(string, string) string = TextDiff

// NormalizeJSON returns an indented json string.
// The keys are sorted lexicographically.
func NormalizeJSONOj(object any) (string, error) {
	data := object
	switch v := object.(type) {
	case string:
		var err error
		var jsonStrMap map[string]any
		err = oj.Unmarshal([]byte(v), &jsonStrMap)

		if err != nil {
			return "", err
		}
		data = jsonStrMap
	}

	out, err := oj.Marshal(data, &ojg.Options{
		Indent:      2,
		Sort:        true,
		OmitNil:     true,
		UseTags:     true,
		FloatFormat: "%0.0f",
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// normalizeJSON returns an indented json string.
// The keys are sorted lexicographically.
func NormalizeJSON(object any) (string, error) {
	data := object
	switch v := object.(type) {
	case string:
		var jsonStrMap map[string]any
		if err := json.Unmarshal([]byte(v), &jsonStrMap); err != nil {
			return "", err
		}
		data = jsonStrMap
	}

	jsonStrIndented, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return "", err
	}

	return string(jsonStrIndented), nil
}

// generateDiff calculates the diff (git style) between the given 2 configs.
func GenerateDiff(ctx dutyContext.Context, newConf, prevConfig string) (string, error) {
	if ctx.Properties().On(false, "scraper.diff.disable") {
		return "", nil
	}

	return generateDiff(newConf, prevConfig)
}

func generateDiff(newConf, prevConfig string) (string, error) {
	if newConf == prevConfig {
		return "", nil
	}

	normalizer := NormalizeJSONOj

	// We want a nicely indented json config with each key-vals in new line
	// because that gives us a better diff. A one-line json string config produces diff
	// that's not very helpful.
	before, err := normalizer(prevConfig)
	if err != nil {
		return "", fmt.Errorf("failed to normalize json for previous config: %w", err)
	}

	after, err := normalizer(newConf)
	if err != nil {
		return "", fmt.Errorf("failed to normalize json for new config: %w", err)
	}

	if before == after {
		return "", nil
	}

	// If we compile the code with rustdiffgen tag, we still might
	// want to disable rust invokation
	var once sync.Once
	once.Do(func() {
		if properties.On(false, "diff.rust-gen") {
			DiffFunc = TextDiff
		}
	})

	return DiffFunc(before, after), nil
}

func TextDiff(before, after string) string {
	edits := myers.ComputeEdits("", before, after)
	if len(edits) == 0 {
		return ""
	}
	return fmt.Sprint(gotextdiff.ToUnified("before", "after", before, edits))
}
