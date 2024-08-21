package db

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	dutyContext "github.com/flanksource/duty/context"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/ohler55/ojg"
	"github.com/ohler55/ojg/oj"
)

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

	return oj.JSON(data, ojg.GoOptions), nil
}

func NormalizeJSONJQ(object any) (string, error) {

	data := object
	switch v := object.(type) {
	case string:
		cmd := exec.Command("jq", "--sort-keys", ".")
		cmd.Stdin = strings.NewReader(v)
		out, err := cmd.Output()
		return string(out), err
	}

	phase1, _ := json.Marshal(data)

	cmd := exec.Command("jq", "--sort-keys", ".")
	cmd.Stdin = strings.NewReader(string(phase1))
	out, err := cmd.Output()
	return string(out), err
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
func generateDiff(ctx dutyContext.Context, newConf, prevConfig string) (string, error) {
	if ctx.Properties().On(false, "scraper.diff.disable") {
		return "", nil
	}

	if newConf == prevConfig {
		return "", nil
	}

	normalizer := NormalizeJSON
	if name := ctx.Properties().String("scraper.diff.normalizer", "go"); name == "oj" {
		normalizer = NormalizeJSONOj
	} else if name == "jq" {
		normalizer = NormalizeJSONJQ
	}

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

	// Compare again. They might be equal after normalization.
	if newConf == prevConfig {
		return "", nil
	}

	edits := myers.ComputeEdits("", before, after)
	if len(edits) == 0 {
		return "", nil
	}

	diff := fmt.Sprint(gotextdiff.ToUnified("before", "after", before, edits))
	return diff, nil
}
