package changes

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/Jeffail/gabs/v2"
	"github.com/flanksource/config-db/db/models"
	"github.com/samber/lo"
)

type Replacement struct {
	Value string
	Regex *regexp.Regexp
}

type Replacements []Replacement

var tokenizer Replacements

func init() {
	tokenizer = NewReplacements(
		"UUID", `\b[0-9a-f]{8}\b-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-\b[0-9a-f]{12}\b`,
		"TIMESTAMP", `\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`,
		"SHA256", `[a-z0-9]{64}`,
		"NUMBER", `^\d+$`,
	)
}

func NewReplacements(pairs ...string) Replacements {
	var r Replacements
	for i := 0; i < len(pairs)-1; i = i + 2 {
		r = append(r, Replacement{
			Value: pairs[i],
			Regex: regexp.MustCompile(pairs[i+1]),
		})
	}
	return r
}

func Fingerprint(change *models.ConfigChange) (string, error) {
	if change == nil {
		return "", nil
	}

	if change.Patches == nil && change.Details == nil {
		return "", nil
	}

	input := []byte(lo.FromPtr(change.Patches))
	if len(input) == 0 {
		detailsJSON, err := json.Marshal(change.Details)
		if err != nil {
			return "", err
		}
		input = detailsJSON
	}

	container, err := gabs.ParseJSON(input)
	if err != nil {
		return "", err
	}

	flat, err := container.Flatten()
	if err != nil {
		return "", err
	}

	var out = make(map[string]interface{})
	for k, v := range flat {
		out[k] = tokenizer.Tokenize(v)
	}

	hash := Hash(out)
	return hash, nil
}

func Hash(data map[string]interface{}) string {
	keys := lo.Keys(data)
	sort.Strings(keys)
	h := md5.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte(data[k].(string)))
	}

	return hex.EncodeToString(h.Sum(nil)[:])
}

func (replacements Replacements) Tokenize(data interface{}) string {
	switch v := data.(type) {

	case int, int8, int16, int32, int64, float32, float64, uint, uint8, uint16, uint32, uint64:
		return "0"

	case time.Time:
		return "TIMESTAMP"
	case string:
		out := v
		for _, r := range replacements {
			out = r.Regex.ReplaceAllString(out, r.Value)
			if out == r.Value {
				break
			}
		}
		return out

	}

	return fmt.Sprintf("%v", data)
}
