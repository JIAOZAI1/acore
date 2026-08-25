// Package jsoncheck provides focused validation for JSON values.
package jsoncheck

import "encoding/json"

// IsObject reports whether value contains a valid JSON object.
func IsObject(value []byte) bool {
	if len(value) == 0 || !json.Valid(value) {
		return false
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(value, &object); err != nil {
		return false
	}
	return object != nil
}
