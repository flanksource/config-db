package v1

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/flanksource/duty/types"
)

// +kubebuilder:object:generate=false
type JSON map[string]interface{}

func NewJSON(v interface{}) JSON {
	j := JSON{}
	switch v := v.(type) {
	case string:
		_ = json.Unmarshal([]byte(v), &j)
	case []byte:
		_ = json.Unmarshal(v, &j)
	default:
		data, _ := json.Marshal(v)
		_ = json.Unmarshal(data, &j)
	}
	return j
}

// Value return json value, implement driver.Valuer interface
func (m JSON) Value() (driver.Value, error) {
	if m == nil {
		return nil, nil
	}
	ba, err := m.MarshalJSON()
	return string(ba), err
}

// Scan scan value into Jsonb, implements sql.Scanner interface
func (m *JSON) Scan(val interface{}) error {
	if val == nil {
		*m = make(JSON)
		return nil
	}
	var ba []byte
	switch v := val.(type) {
	case []byte:
		ba = v
	case string:
		ba = []byte(v)
	default:
		return errors.New(fmt.Sprint("Failed to unmarshal JSONB value:", val))
	}
	t := map[string]interface{}{}
	err := json.Unmarshal(ba, &t)
	*m = t
	return err
}

// MarshalJSON to output non base64 encoded []byte
func (m JSON) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	t := (map[string]interface{})(m)
	return json.Marshal(t)
}

// UnmarshalJSON to deserialize []byte
func (m *JSON) UnmarshalJSON(b []byte) error {
	t := map[string]interface{}{}
	err := json.Unmarshal(b, &t)
	*m = JSON(t)
	return err
}

// GormDataType gorm common data type
func (m JSON) GormDataType() string {
	return "json"
}

// JSONStringMap defiend JSON data type, need to implements driver.Valuer, sql.Scanner interface
type JSONStringMap types.JSONStringMap
