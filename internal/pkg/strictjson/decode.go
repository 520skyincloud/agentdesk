package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	ErrorJSONTooLarge          = "json_too_large"
	ErrorJSONInvalidUTF8       = "json_invalid_utf8"
	ErrorJSONRootNotObject     = "json_root_not_object"
	ErrorJSONSyntaxInvalid     = "json_syntax_invalid"
	ErrorJSONDuplicateKey      = "json_duplicate_key"
	ErrorJSONUnknownField      = "json_unknown_field"
	ErrorJSONTrailingContent   = "json_trailing_content"
	ErrorJSONSchemaInvalid     = "json_schema_invalid"
	ErrorJSONReferenceInvalid  = "json_reference_invalid"
	ErrorJSONBusinessInvariant = "json_business_invariant"
)

type DecodeOptions struct {
	MaxBytes int64
	Schema   []byte
}

type ProtocolError struct {
	Code    string
	Path    string
	Message string
	Err     error
}

func (e *ProtocolError) Error() string {
	if e == nil {
		return ""
	}
	message := strings.TrimSpace(e.Message)
	if message == "" && e.Err != nil {
		message = e.Err.Error()
	}
	if e.Path != "" {
		return fmt.Sprintf("%s at %s: %s", e.Code, e.Path, message)
	}
	return fmt.Sprintf("%s: %s", e.Code, message)
}

func (e *ProtocolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func CodeOf(err error) (string, bool) {
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) || protocolErr == nil {
		return "", false
	}
	return protocolErr.Code, protocolErr.Code != ""
}

func PathOf(err error) (string, bool) {
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) || protocolErr == nil {
		return "", false
	}
	path := strings.TrimSpace(protocolErr.Path)
	return path, path != ""
}

func DecodeObject[T any](raw []byte, opts DecodeOptions) (T, error) {
	var zero T
	if opts.MaxBytes > 0 && int64(len(raw)) > opts.MaxBytes {
		return zero, &ProtocolError{Code: ErrorJSONTooLarge, Message: fmt.Sprintf("payload is %d bytes, limit is %d", len(raw), opts.MaxBytes)}
	}
	if !utf8.Valid(raw) {
		return zero, &ProtocolError{Code: ErrorJSONInvalidUTF8, Message: "payload is not valid UTF-8"}
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return zero, &ProtocolError{Code: ErrorJSONRootNotObject, Path: "$", Message: "root JSON value must be an object"}
	}
	if err := RejectDuplicateObjectKeys(trimmed); err != nil {
		return zero, err
	}

	var decoded T
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		code := ErrorJSONSyntaxInvalid
		if strings.Contains(err.Error(), "unknown field") {
			code = ErrorJSONUnknownField
		}
		return zero, &ProtocolError{Code: code, Path: "$", Message: err.Error(), Err: err}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return zero, &ProtocolError{Code: ErrorJSONTrailingContent, Path: "$", Message: "root object is followed by another JSON value"}
		}
		return zero, &ProtocolError{Code: ErrorJSONTrailingContent, Path: "$", Message: err.Error(), Err: err}
	}
	if len(opts.Schema) > 0 {
		if err := ValidateSchema(trimmed, opts.Schema); err != nil {
			return zero, err
		}
	}
	return decoded, nil
}
