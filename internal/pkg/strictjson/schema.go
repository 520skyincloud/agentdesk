package strictjson

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	jsonschema "github.com/google/jsonschema-go/jsonschema"
)

var resolvedSchemaCache sync.Map

func ValidateSchema(raw, schemaRaw []byte) error {
	resolved, err := compileSchema(schemaRaw)
	if err != nil {
		return &ProtocolError{Code: ErrorJSONSchemaInvalid, Path: "$schema", Message: err.Error(), Err: err}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var instance any
	if err := decoder.Decode(&instance); err != nil {
		return &ProtocolError{Code: ErrorJSONSyntaxInvalid, Path: "$", Message: err.Error(), Err: err}
	}
	if err := resolved.Validate(instance); err != nil {
		return &ProtocolError{Code: ErrorJSONSchemaInvalid, Path: "$", Message: err.Error(), Err: err}
	}
	return nil
}

func ValidateSchemaDefinition(schemaRaw []byte) error {
	_, err := compileSchema(schemaRaw)
	return err
}

func compileSchema(schemaRaw []byte) (*jsonschema.Resolved, error) {
	if len(schemaRaw) == 0 {
		return nil, fmt.Errorf("schema is empty")
	}
	sum := sha256.Sum256(schemaRaw)
	key := hex.EncodeToString(sum[:])
	if cached, ok := resolvedSchemaCache.Load(key); ok {
		return cached.(*jsonschema.Resolved), nil
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, fmt.Errorf("resolve schema: %w", err)
	}
	actual, _ := resolvedSchemaCache.LoadOrStore(key, resolved)
	return actual.(*jsonschema.Resolved), nil
}
