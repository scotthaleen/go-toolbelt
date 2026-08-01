// Package strictjson validates and decodes one bounded JSON value without
// accepting duplicate object member names.
//
// Validate accepts any JSON top-level value, checks every object iteratively,
// and rejects additional trailing values. Decode performs that structural
// validation before applying encoding/json's normal typed decoding rules:
//
//	var request struct {
//		Name string `json:"name"`
//	}
//	err := strictjson.Decode(data, &request, strictjson.DisallowUnknownFields())
//
// Use DecodeReader at input boundaries so reads and structural validation memory
// are bounded by a positive caller-selected maximum:
//
//	err := strictjson.DecodeReader(r, 1<<20, &request)
//
// Errors classify duplicate names, unknown fields, invalid JSON, oversized
// input, and destination decode failures without including JSON member names or
// values in their text. They are structural classifications suitable for
// errors.Is, not schema, authorization, or user-facing diagnostics. Required
// fields, schemas, tagged unions, semantic validation, authorization, and
// transport policy remain application concerns.
//
// This package uses only the Go standard library.
package strictjson
