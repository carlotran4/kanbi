package diagnostics

import "encoding/json"

func bindJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}
