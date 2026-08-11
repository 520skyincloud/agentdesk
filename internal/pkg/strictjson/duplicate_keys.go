package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func RejectDuplicateObjectKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := scanJSONValue(decoder, "$"); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return &ProtocolError{Code: ErrorJSONTrailingContent, Path: "$", Message: "root object is followed by trailing content"}
		}
		return &ProtocolError{Code: ErrorJSONTrailingContent, Path: "$", Message: err.Error(), Err: err}
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return &ProtocolError{Code: ErrorJSONSyntaxInvalid, Path: path, Message: err.Error(), Err: err}
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return &ProtocolError{Code: ErrorJSONSyntaxInvalid, Path: path, Message: keyErr.Error(), Err: keyErr}
			}
			key, ok := keyToken.(string)
			if !ok {
				return &ProtocolError{Code: ErrorJSONSyntaxInvalid, Path: path, Message: "object key is not a string"}
			}
			keyPath := joinJSONPointer(path, key)
			if _, exists := seen[key]; exists {
				return &ProtocolError{Code: ErrorJSONDuplicateKey, Path: keyPath, Message: fmt.Sprintf("duplicate object key %q", key)}
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder, keyPath); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return &ProtocolError{Code: ErrorJSONSyntaxInvalid, Path: path, Message: err.Error(), Err: err}
		}
	case '[':
		index := 0
		for decoder.More() {
			if err := scanJSONValue(decoder, path+"/"+strconv.Itoa(index)); err != nil {
				return err
			}
			index++
		}
		if _, err := decoder.Token(); err != nil {
			return &ProtocolError{Code: ErrorJSONSyntaxInvalid, Path: path, Message: err.Error(), Err: err}
		}
	default:
		return &ProtocolError{Code: ErrorJSONSyntaxInvalid, Path: path, Message: fmt.Sprintf("unexpected delimiter %q", delim)}
	}
	return nil
}

func joinJSONPointer(path, key string) string {
	key = strings.ReplaceAll(key, "~", "~0")
	key = strings.ReplaceAll(key, "/", "~1")
	return path + "/" + key
}
