package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

var (
	// ErrTooLarge indicates that reader input exceeds the caller-supplied limit.
	ErrTooLarge = errors.New("strictjson: input exceeds maximum size")
	// ErrDuplicateName indicates that an object contains a repeated member name.
	ErrDuplicateName = errors.New("strictjson: duplicate object member name")
	// ErrUnknownField indicates that a destination struct does not accept an object member.
	ErrUnknownField = errors.New("strictjson: unknown object member")
	// ErrInvalidJSON indicates malformed, empty, or multiple JSON values.
	ErrInvalidJSON = errors.New("strictjson: invalid JSON")
	// ErrDecode indicates that the destination rejected an otherwise valid JSON value.
	ErrDecode = errors.New("strictjson: destination rejected JSON value")

	errNilReader = errors.New("strictjson: reader cannot be nil")
	errNilTarget = errors.New("strictjson: destination cannot be nil")
)

type decodeOptions struct {
	disallowUnknownFields bool
}

// Option configures Decode and DecodeReader. Options are created by this package.
type Option interface {
	apply(*decodeOptions)
}

type optionFunc func(*decodeOptions)

func (fn optionFunc) apply(opts *decodeOptions) {
	fn(opts)
}

// DisallowUnknownFields rejects object members that do not match an exported
// field in the destination struct.
func DisallowUnknownFields() Option {
	return optionFunc(func(opts *decodeOptions) {
		opts.disallowUnknownFields = true
	})
}

// Validate checks that data contains exactly one JSON value and that no object
// contains duplicate member names. Errors do not include member names or values.
func Validate(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return validateTokens(decoder)
}

// Decode strictly validates data before decoding it into dst using
// encoding/json's normal typed decoding rules. Errors containing JSON member
// names or values are replaced with stable package classifications.
func Decode(data []byte, dst any, optionFns ...Option) error {
	if isNilPointer(dst) {
		return errNilTarget
	}
	opts, err := applyOptions(optionFns)
	if err != nil {
		return err
	}
	return decode(data, dst, opts)
}

// DecodeReader reads at most maxBytes+1 bytes from r, rejects oversized input,
// and otherwise behaves like Decode. maxBytes must be positive.
func DecodeReader(r io.Reader, maxBytes int64, dst any, optionFns ...Option) error {
	if isNilPointer(r) {
		return errNilReader
	}
	if maxBytes <= 0 {
		return errors.New("strictjson: maximum size must be positive")
	}
	if isNilPointer(dst) {
		return errNilTarget
	}
	opts, err := applyOptions(optionFns)
	if err != nil {
		return err
	}

	limited := &io.LimitedReader{R: r, N: maxBytes}
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("strictjson: read input: %w", err)
	}
	if limited.N == 0 {
		var extra [1]byte
		n, readErr := r.Read(extra[:])
		if n > 0 {
			return ErrTooLarge
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("strictjson: read input: %w", readErr)
		}
		if n == 0 && readErr == nil {
			return fmt.Errorf("strictjson: read input: %w", io.ErrNoProgress)
		}
	}
	return decode(data, dst, opts)
}

type containerFrame struct {
	delimiter json.Delim
	seen      map[string]struct{}
	wantKey   bool
}

// Keep token scanning aligned with encoding/json's scanner depth limit.
const maxNestingDepth = 10000

func validateTokens(decoder *json.Decoder) error {
	frames := make([]containerFrame, 0, 16)
	rootComplete := false

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if rootComplete && len(frames) == 0 {
				return nil
			}
			return ErrInvalidJSON
		}
		if err != nil {
			return ErrInvalidJSON
		}
		if rootComplete && len(frames) == 0 {
			return ErrInvalidJSON
		}

		if len(frames) > 0 {
			frame := &frames[len(frames)-1]
			if frame.delimiter == '{' && frame.wantKey {
				if delimiter, ok := token.(json.Delim); ok && delimiter == '}' {
					frames = frames[:len(frames)-1]
					if len(frames) == 0 {
						rootComplete = true
					}
					continue
				}
				key, ok := token.(string)
				if !ok {
					return ErrInvalidJSON
				}
				if _, exists := frame.seen[key]; exists {
					return ErrDuplicateName
				}
				frame.seen[key] = struct{}{}
				frame.wantKey = false
				continue
			}
		}

		delimiter, isDelimiter := token.(json.Delim)
		if isDelimiter && (delimiter == '}' || delimiter == ']') {
			if len(frames) == 0 || !delimitersMatch(frames[len(frames)-1].delimiter, delimiter) {
				return ErrInvalidJSON
			}
			frames = frames[:len(frames)-1]
			if len(frames) == 0 {
				rootComplete = true
			}
			continue
		}

		if len(frames) > 0 && frames[len(frames)-1].delimiter == '{' {
			frames[len(frames)-1].wantKey = true
		}

		if isDelimiter {
			if len(frames) == maxNestingDepth {
				return ErrInvalidJSON
			}
			switch delimiter {
			case '{':
				frames = append(frames, containerFrame{
					delimiter: '{',
					seen:      make(map[string]struct{}),
					wantKey:   true,
				})
			case '[':
				frames = append(frames, containerFrame{delimiter: '['})
			default:
				return ErrInvalidJSON
			}
			continue
		}
		if len(frames) == 0 {
			rootComplete = true
		}
	}
}

func decode(data []byte, dst any, opts decodeOptions) error {
	if err := Validate(data); err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	if opts.disallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(dst); err != nil {
		return sanitizeDecodeError(err)
	}
	return nil
}

func applyOptions(optionFns []Option) (decodeOptions, error) {
	var opts decodeOptions
	for _, optionFn := range optionFns {
		if isNilOption(optionFn) {
			return decodeOptions{}, errors.New("strictjson: option cannot be nil")
		}
		optionFn.apply(&opts)
	}
	return opts, nil
}

func delimitersMatch(open, close json.Delim) bool {
	return open == '{' && close == '}' || open == '[' && close == ']'
}

func sanitizeDecodeError(err error) error {
	if strings.HasPrefix(err.Error(), "json: unknown field ") {
		return ErrUnknownField
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		redacted := *typeErr
		redacted.Struct = ""
		redacted.Field = ""
		if before, _, found := strings.Cut(redacted.Value, " "); found {
			redacted.Value = before
		}
		return &decodeError{cause: &redacted}
	}

	var invalidErr *json.InvalidUnmarshalError
	if errors.As(err, &invalidErr) {
		return invalidErr
	}
	return &decodeError{cause: err}
}

type decodeError struct {
	cause error
}

func (e *decodeError) Error() string {
	return ErrDecode.Error()
}

func (e *decodeError) Is(target error) bool {
	return target == ErrDecode || errors.Is(e.cause, target)
}

func (e *decodeError) Unwrap() error {
	return e.cause
}

func isNilPointer(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	return reflected.Kind() == reflect.Pointer && reflected.IsNil()
}

func isNilOption(option Option) bool {
	if option == nil {
		return true
	}
	reflected := reflect.ValueOf(option)
	switch reflected.Kind() {
	case reflect.Func, reflect.Interface, reflect.Pointer:
		return reflected.IsNil()
	default:
		return false
	}
}
