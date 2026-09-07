package feedback_test

import "encoding/json"

// One place the test file's JSON reading goes through, so the enumeration test
// above reads as a statement about the document rather than about encoding.
func jsonUnmarshalImpl(data []byte, target any) error { return json.Unmarshal(data, target) }
