package gcp

import (
	"fmt"
	"slices"

	"google.golang.org/protobuf/types/known/structpb"
)

// Recursively applies f to all string values in the Struct
func applyFuncToAllStructPBStrings(s *structpb.Struct, f func(string) string, fieldNames ...string) {
	if s == nil {
		return
	}

	for key, val := range s.Fields {
		switch kind := val.Kind.(type) {

		case *structpb.Value_StringValue:
			// Transform the string value
			if (len(fieldNames) > 0 && slices.Contains(fieldNames, key)) || len(fieldNames) == 0 {
				s.Fields[key] = structpb.NewStringValue(f(kind.StringValue))
			}

		case *structpb.Value_StructValue:
			// Recurse into nested Struct
			applyFuncToAllStructPBStrings(kind.StructValue, f, fieldNames...)

		case *structpb.Value_ListValue:
			// Recurse into list
			if (len(fieldNames) > 0 && slices.Contains(fieldNames, key)) || len(fieldNames) == 0 {
				applyFuncToList(kind.ListValue, f, fieldNames...)
			}

		// Other types (number, bool, null): do nothing
		default:
			continue
		}
	}
}

// Helper: recursively applies f to strings inside a ListValue
func applyFuncToList(list *structpb.ListValue, f func(string) string, fieldNames ...string) {
	for i, val := range list.Values {
		switch kind := val.Kind.(type) {
		case *structpb.Value_StringValue:
			list.Values[i] = structpb.NewStringValue(f(kind.StringValue))
		case *structpb.Value_StructValue:
			applyFuncToAllStructPBStrings(kind.StructValue, f, fieldNames...)
		case *structpb.Value_ListValue:
			applyFuncToList(kind.ListValue, f, fieldNames...)
		default:
			continue
		}
	}
}

// removeFields recursively removes specified field names from a structpb.Struct
func removeFields(s *structpb.Struct, fieldsToRemove ...string) {
	if s == nil || s.Fields == nil {
		return
	}

	// Create a set for O(1) lookup
	fieldSet := make(map[string]bool)
	for _, field := range fieldsToRemove {
		fieldSet[field] = true
	}

	// Remove fields from current level
	for fieldName := range s.Fields {
		if fieldSet[fieldName] {
			delete(s.Fields, fieldName)
		}
	}

	// Recursively process nested structures
	for _, value := range s.Fields {
		removeFieldsFromValue(value, fieldsToRemove, fieldSet)
	}
}

// removeFieldsFromValue handles different types of structpb.Value
func removeFieldsFromValue(v *structpb.Value, fieldsToRemove []string, fieldSet map[string]bool) {
	if v == nil {
		return
	}

	switch kind := v.GetKind().(type) {
	case *structpb.Value_StructValue:
		// Recursively process nested struct
		removeFields(kind.StructValue, fieldsToRemove...)

	case *structpb.Value_ListValue:
		// Process each item in the list
		if kind.ListValue != nil && kind.ListValue.Values != nil {
			for _, item := range kind.ListValue.Values {
				removeFieldsFromValue(item, fieldsToRemove, fieldSet)
			}
		}

	// Other types (string, number, bool, null) don't contain nested structures
	default:
		// No nested structures to process
	}
}

// AddStructArrayField adds a new field to struct 'a' with fieldName and value as slice of structs 'b'
func AddStructArrayField(a *structpb.Struct, b []*structpb.Struct, fieldName string) error {
	if a == nil {
		return fmt.Errorf("target struct cannot be nil")
	}

	// Initialize Fields map if it doesn't exist
	if a.Fields == nil {
		a.Fields = make(map[string]*structpb.Value)
	}

	// Convert slice of structs to slice of structpb.Value
	values := make([]*structpb.Value, len(b))
	for i, structItem := range b {
		if structItem == nil {
			// Handle nil structs as null values
			values[i] = structpb.NewNullValue()
		} else {
			values[i] = structpb.NewStructValue(structItem)
		}
	}

	// Create ListValue from the slice of values
	listValue := &structpb.ListValue{
		Values: values,
	}

	// Add the field to struct 'a'
	a.Fields[fieldName] = structpb.NewListValue(listValue)

	return nil
}
