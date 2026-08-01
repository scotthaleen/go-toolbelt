package strictjson_test

import (
	"errors"
	"fmt"
	"strings"

	"github.com/scotthaleen/go-toolbelt/strictjson"
)

func ExampleDecodeReader() {
	type request struct {
		Name string `json:"name"`
	}

	var value request
	err := strictjson.DecodeReader(
		strings.NewReader(`{"name":"widget"}`),
		1024,
		&value,
		strictjson.DisallowUnknownFields(),
	)
	fmt.Println(value.Name, err)
	// Output: widget <nil>
}

func ExampleErrUnknownField() {
	var value struct {
		Name string `json:"name"`
	}
	err := strictjson.Decode(
		[]byte(`{"name":"widget","private-token":"redacted"}`),
		&value,
		strictjson.DisallowUnknownFields(),
	)
	fmt.Println(errors.Is(err, strictjson.ErrUnknownField), err)
	// Output: true strictjson: unknown object member
}
