package settings

import (
	"bytes"
	"encoding/json"
	"errors"
)

// ExportV2 produces a rollback copy without changing the current v3 state. It
// removes only locale fields, retaining all other settings, secrets and journals.
// The returned document contains credentials and must be stored privately.
func ExportV2(data []byte) ([]byte, error) {
	if len(data) > 4<<20 {
		return nil, errors.New("settings state exceeds size limit")
	}
	state, err := decodeStrict[diskState](data)
	if err != nil {
		return nil, errors.New("invalid settings state")
	}
	if err := (&Store{}).validateState(state); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, errors.New("cannot encode settings state")
	}
	var doc map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&doc); err != nil {
		return nil, err
	}
	removeLocale := func(values any) {
		v := values.(map[string]any)
		delete(v["screen"].(map[string]any), "locale")
	}
	removeLocale(doc["baseline"])
	for _, value := range doc["profiles"].(map[string]any) {
		removeLocale(value.(map[string]any)["settings"])
	}
	doc["version"] = 2
	return json.MarshalIndent(doc, "", "  ")
}
