package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
	"testing/iotest"
)

func TestDecode(t *testing.T) {
	type request struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	var got request
	if err := Decode([]byte(`{"name":"widget","count":2,"extra":true}`), &got); err != nil {
		t.Fatal(err)
	}
	if got != (request{Name: "widget", Count: 2}) {
		t.Fatalf("Decode() = %#v", got)
	}
	if err := Decode([]byte(`{"name":"widget","count":2,"extra":true}`), &got, DisallowUnknownFields()); !errors.Is(err, ErrUnknownField) {
		t.Fatalf("Decode() error = %v, want ErrUnknownField", err)
	}

	var typeErr *json.UnmarshalTypeError
	if err := Decode([]byte(`{"count":"many"}`), &got); !errors.Is(err, ErrDecode) || !errors.As(err, &typeErr) {
		t.Fatalf("Decode() error = %T %v, want *json.UnmarshalTypeError", err, err)
	} else if typeErr.Struct != "" || typeErr.Field != "" {
		t.Fatalf("Decode() type error exposed destination names: %#v", typeErr)
	}
}

func TestValidateDuplicateNames(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"envelope", `{"kind":"x","kind":"y"}`},
		{"nested", `{"outer":{"id":1,"id":2}}`},
		{"array", `[{"id":1,"id":2}]`},
		{"nested array", `{"items":[0,{"id":1,"id":2}]}`},
		{"escaped equivalent", `{"name":1,"na\u006de":2}`},
		{"escaped surrogate equivalent", `{"�":1,"\ud800":2}`},
		{"after unrelated values", `{"id":1,"nested":{"ok":true},"items":[1,2],"id":2}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate([]byte(test.input))
			if !errors.Is(err, ErrDuplicateName) {
				t.Fatalf("Validate() error = %T %v, want ErrDuplicateName", err, err)
			}
		})
	}
}

func TestValidateTopLevelValues(t *testing.T) {
	valid := []string{
		`null`,
		`true`,
		`false`,
		`"text"`,
		`0`,
		`-1.25e+10000`,
		`[]`,
		`[1,"two",false,null,{}]`,
		`{}`,
		`{"nested":{"array":[1,2,3]}}`,
	}
	for _, input := range valid {
		if err := Validate([]byte(input)); err != nil {
			t.Errorf("Validate(%q) error = %v", input, err)
		}
	}
}

func TestValidateMalformedAndTrailing(t *testing.T) {
	invalid := []string{
		``,
		` `,
		`{`,
		`[`,
		`{"a":`,
		`{"a":1`,
		`[1,`,
		`{"a",1}`,
		`{"a":01}`,
		`"unterminated`,
		`tru`,
		`null null`,
		`{}[]`,
		`1 false`,
		string([]byte{'"', 0xff, '"', 'x'}),
	}
	for _, input := range invalid {
		if err := Validate([]byte(input)); !errors.Is(err, ErrInvalidJSON) {
			t.Errorf("Validate(%q) error = %v, want ErrInvalidJSON", input, err)
		}
	}
}

func TestValidateDeepNestingUsesBoundedFrames(t *testing.T) {
	const depth = 9000
	input := strings.Repeat("[", depth) + "null" + strings.Repeat("]", depth)
	if err := Validate([]byte(input)); err != nil {
		t.Fatalf("Validate() deep nesting error = %v", err)
	}
	tooDeep := strings.Repeat("[", 10001) + "null" + strings.Repeat("]", 10001)
	if err := Validate([]byte(tooDeep)); !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("Validate() beyond encoding/json nesting limit error = %v, want ErrInvalidJSON", err)
	}

	shallowInput := strings.Repeat("[", depth/2) + "null" + strings.Repeat("]", depth/2)
	shallowBytes := validateAllocatedBytes(t, shallowInput)
	deepBytes := validateAllocatedBytes(t, input)
	if deepBytes > shallowBytes*3 {
		t.Fatalf("Validate() allocation growth = %d to %d bytes when depth doubles, want linear growth", shallowBytes, deepBytes)
	}
	if max := int64(128 * len(input)); deepBytes > max {
		got := deepBytes
		t.Fatalf("Validate() allocated %d bytes for %d input bytes, want <= %d", got, len(input), max)
	}
}

func validateAllocatedBytes(t *testing.T, input string) int64 {
	t.Helper()
	result := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			if err := Validate([]byte(input)); err != nil {
				b.Fatal(err)
			}
		}
	})
	return result.AllocedBytesPerOp()
}

func TestDecodeRejectsInvalidArguments(t *testing.T) {
	var target map[string]any
	if err := Decode([]byte(`{}`), nil); err == nil {
		t.Fatal("Decode() nil destination error = nil")
	}
	var pointer *map[string]any
	if err := Decode([]byte(`{}`), pointer); err == nil {
		t.Fatal("Decode() typed nil destination error = nil")
	}
	if err := Decode([]byte(`{}`), &target, nil); err == nil {
		t.Fatal("Decode() nil option error = nil")
	}
	if err := Decode([]byte(`{} {}`), &target); err == nil {
		t.Fatal("Decode() trailing value error = nil")
	}
}

func TestDecodeReaderValidatesOptionsBeforeReading(t *testing.T) {
	reader := &countingReader{reader: strings.NewReader(`null`)}
	var target any
	if err := DecodeReader(reader, 10, &target, nil); err == nil {
		t.Fatal("DecodeReader() nil option error = nil")
	}
	if reader.read != 0 {
		t.Fatalf("DecodeReader() read %d bytes before rejecting options", reader.read)
	}
}

func TestDecodeReaderBounds(t *testing.T) {
	input := []byte(`{"name":"ok"}`)
	var target map[string]string
	if err := DecodeReader(bytes.NewReader(input), int64(len(input)), &target); err != nil {
		t.Fatalf("DecodeReader() exact limit error = %v", err)
	}
	if target["name"] != "ok" {
		t.Fatalf("DecodeReader() target = %#v", target)
	}

	counting := &countingReader{reader: bytes.NewReader(input)}
	if err := DecodeReader(counting, int64(len(input)-1), &target); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("DecodeReader() error = %v, want ErrTooLarge", err)
	}
	if counting.read != int64(len(input)) {
		t.Fatalf("DecodeReader() read %d bytes, want max+1 = %d", counting.read, len(input))
	}

	var scalar any
	if err := DecodeReader(bytes.NewReader([]byte(`null`)), math.MaxInt64, &scalar); err != nil {
		t.Fatalf("DecodeReader() MaxInt64 error = %v", err)
	}
}

func TestDecodeReaderErrorsAndArguments(t *testing.T) {
	wantErr := errors.New("read failed")
	var target any
	if err := DecodeReader(iotest.ErrReader(wantErr), 10, &target); !errors.Is(err, wantErr) {
		t.Fatalf("DecodeReader() error = %v, want wrapped read error", err)
	}

	for _, maxBytes := range []int64{0, -1} {
		if err := DecodeReader(strings.NewReader(`null`), maxBytes, &target); err == nil {
			t.Errorf("DecodeReader(maxBytes=%d) error = nil", maxBytes)
		}
	}
	if err := DecodeReader(nil, 10, &target); err == nil {
		t.Fatal("DecodeReader() nil reader error = nil")
	}
	var reader *bytes.Reader
	if err := DecodeReader(reader, 10, &target); err == nil {
		t.Fatal("DecodeReader() typed nil reader error = nil")
	}
	if err := DecodeReader(strings.NewReader(`null`), 10, nil); err == nil {
		t.Fatal("DecodeReader() nil destination error = nil")
	}
}

func TestErrorsDoNotExposeValues(t *testing.T) {
	const sensitive = "very-secret-value"
	const sensitiveNumber = "987654321098765432109876543210"
	type request struct {
		Count int `json:"count"`
	}
	inputs := []struct {
		data string
		dst  any
		opts []Option
	}{
		{`{"token":"` + sensitive + `","token":"other"}`, &map[string]any{}, nil},
		{`{"count":"` + sensitive + `"}`, &request{}, nil},
		{`{"known":1,"extra":"` + sensitive + `"}`, &struct {
			Known int `json:"known"`
		}{}, []Option{DisallowUnknownFields()}},
		{`{"` + sensitive + `":1,"` + sensitive + `":2}`, &map[string]any{}, nil},
		{`{"outer-` + sensitive + `":{"duplicate":1,"duplicate":2}}`, &map[string]any{}, nil},
		{`{"known":1,"` + sensitive + `":2}`, &struct {
			Known int `json:"known"`
		}{}, []Option{DisallowUnknownFields()}},
		{`{"count":` + sensitiveNumber + `}`, &request{}, nil},
		{`{"token":"` + sensitive + `"} trailing`, &map[string]any{}, nil},
	}
	for _, test := range inputs {
		err := Decode([]byte(test.data), test.dst, test.opts...)
		if err == nil {
			t.Fatalf("Decode(%q) error = nil", test.data)
		}
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("Decode() error exposes sensitive value: %v", err)
		}
		if strings.Contains(err.Error(), sensitiveNumber) {
			t.Fatalf("Decode() error exposes sensitive number: %v", err)
		}
	}
}

func TestDecodePreservesRedactedErrorClassifications(t *testing.T) {
	type request struct {
		Count int `json:"count"`
	}
	var target request
	err := Decode([]byte(`{"count":987654321098765432109876543210}`), &target)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("Decode() error = %T %v, want *json.UnmarshalTypeError", err, err)
	}
	if typeErr.Value != "number" {
		t.Fatalf("UnmarshalTypeError.Value = %q, want redacted number classification", typeErr.Value)
	}

	wantErr := errors.New("destination sentinel")
	var custom rejectingValue
	custom.err = wantErr
	err = Decode([]byte(`"secret value"`), &custom)
	if !errors.Is(err, ErrDecode) || !errors.Is(err, wantErr) {
		t.Fatalf("Decode() error = %v, want ErrDecode and destination sentinel", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("Decode() error exposes custom decoder text: %v", err)
	}
}

func FuzzValidate(f *testing.F) {
	seeds := [][]byte{
		[]byte(`null`),
		[]byte(`{"a":1}`),
		[]byte(`{"a":1,"a":2}`),
		[]byte(`[true,{"nested":"value"}]`),
		[]byte(`{"truncated":`),
		{0xff, 0x00, '{'},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		err := Validate(data)
		if err == nil && !json.Valid(data) {
			t.Fatalf("Validate() accepted input rejected by encoding/json: %q", data)
		}
		var dst any
		decodeErr := Decode(data, &dst)
		if err != nil && decodeErr == nil {
			t.Fatalf("Decode() accepted input rejected by Validate: %q", data)
		}
	})
}

func FuzzDuplicateNames(f *testing.F) {
	f.Add([]byte("name"))
	f.Add([]byte{0, 1, 2, 0xff})
	f.Fuzz(func(t *testing.T, seed []byte) {
		if len(seed) > 64 {
			seed = seed[:64]
		}
		key := fmt.Sprintf("secret-%x", seed)
		plain, err := json.Marshal(key)
		if err != nil {
			t.Fatal(err)
		}
		escaped := escapedJSONString(key)
		input := []byte(fmt.Sprintf(`{"wrapper":[{%s:1,%s:2}]}`, plain, escaped))
		if err := Validate(input); !errors.Is(err, ErrDuplicateName) {
			t.Fatalf("Validate(%q) error = %v, want ErrDuplicateName", input, err)
		}
	})
}

func escapedJSONString(value string) string {
	var escaped strings.Builder
	escaped.WriteByte('"')
	for index := range len(value) {
		fmt.Fprintf(&escaped, `\u%04x`, value[index])
	}
	escaped.WriteByte('"')
	return escaped.String()
}

func FuzzDecodeReaderBounds(f *testing.F) {
	f.Add([]byte("value"))
	f.Add([]byte{0, 1, 2, 0xff})
	f.Fuzz(func(t *testing.T, value []byte) {
		if len(value) > 1024 {
			value = value[:1024]
		}
		input, err := json.Marshal(string(value))
		if err != nil {
			t.Fatal(err)
		}

		var target string
		exact := &countingReader{reader: bytes.NewReader(input)}
		if err := DecodeReader(exact, int64(len(input)), &target); err != nil {
			t.Fatalf("DecodeReader() exact limit error = %v", err)
		}
		if exact.read != int64(len(input)) {
			t.Fatalf("DecodeReader() exact limit read %d bytes, want %d", exact.read, len(input))
		}

		tooLarge := &countingReader{reader: bytes.NewReader(input)}
		limit := int64(len(input) - 1)
		if err := DecodeReader(tooLarge, limit, &target); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("DecodeReader() error = %v, want ErrTooLarge", err)
		}
		if tooLarge.read > limit+1 {
			t.Fatalf("DecodeReader() read %d bytes, want at most %d", tooLarge.read, limit+1)
		}
	})
}

type rejectingValue struct {
	err error
}

func (v *rejectingValue) UnmarshalJSON(data []byte) error {
	return fmt.Errorf("rejected %s: %w", data, v.err)
}

type countingReader struct {
	reader io.Reader
	read   int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += int64(n)
	return n, err
}
