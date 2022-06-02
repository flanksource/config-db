package v1

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// JSONMap defiend JSON data type, need to implements driver.Valuer, sql.Scanner interface
type JSONMap map[string]interface{}

// Value return json value, implement driver.Valuer interface
func (m JSONMap) Value() (driver.Value, error) {
	if m == nil {
		return nil, nil
	}
	ba, err := m.MarshalJSON()
	return string(ba), err
}

// Scan scan value into Jsonb, implements sql.Scanner interface
func (m *JSONMap) Scan(val interface{}) error {
	if val == nil {
		*m = make(JSONMap)
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
func (m JSONMap) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	t := (map[string]interface{})(m)
	return json.Marshal(t)
}

// UnmarshalJSON to deserialize []byte
func (m *JSONMap) UnmarshalJSON(b []byte) error {
	t := map[string]interface{}{}
	err := json.Unmarshal(b, &t)
	*m = JSONMap(t)
	return err
}

// GormDataType gorm common data type
func (m JSONMap) GormDataType() string {
	return "jsonmap"
}

// GormDBDataType gorm db data type
func (JSONMap) GormDBDataType(db *gorm.DB, field *schema.Field) string {
	switch db.Dialector.Name() {
	case "sqlite":
		return "JSON"
	case "postgres":
		return "JSONB"
	case "sqlserver":
		return "NVARCHAR(MAX)"
	}
	return ""
}

func (jm JSONMap) GormValue(ctx context.Context, db *gorm.DB) clause.Expr {
	data, _ := jm.MarshalJSON()
	return gorm.Expr("?", string(data))
}

// JSONStringMap defiend JSON data type, need to implements driver.Valuer, sql.Scanner interface
type JSONStringMap map[string]string

// Value return json value, implement driver.Valuer interface
func (m JSONStringMap) Value() (driver.Value, error) {
	if m == nil {
		return nil, nil
	}
	ba, err := m.MarshalJSON()
	return string(ba), err
}

// Scan scan value into Jsonb, implements sql.Scanner interface
func (m *JSONStringMap) Scan(val interface{}) error {
	if val == nil {
		*m = make(JSONStringMap)
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
	t := map[string]string{}
	err := json.Unmarshal(ba, &t)
	*m = t
	return err
}

// MarshalJSON to output non base64 encoded []byte
func (m JSONStringMap) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	t := (map[string]string)(m)
	return json.Marshal(t)
}

// UnmarshalJSON to deserialize []byte
func (m *JSONStringMap) UnmarshalJSON(b []byte) error {
	t := map[string]string{}
	err := json.Unmarshal(b, &t)
	*m = JSONStringMap(t)
	return err
}

// GormDataType gorm common data type
func (m JSONStringMap) GormDataType() string {
	return "jsonstringmap"
}

// GormDBDataType gorm db data type
func (JSONStringMap) GormDBDataType(db *gorm.DB, field *schema.Field) string {
	switch db.Dialector.Name() {
	case "sqlite":
		return "JSON"
	case "postgres":
		return "JSONB"
	case "sqlserver":
		return "NVARCHAR(MAX)"
	}
	return ""
}

func (jm JSONStringMap) GormValue(ctx context.Context, db *gorm.DB) clause.Expr {
	data, _ := jm.MarshalJSON()
	return gorm.Expr("?", string(data))
}
