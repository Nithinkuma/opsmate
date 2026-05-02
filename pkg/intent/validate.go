package intent

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed schema.json
var schemaFS embed.FS

var compiledSchema *jsonschema.Schema

func init() {
	data, err := schemaFS.ReadFile("schema.json")
	if err != nil {
		panic(fmt.Sprintf("intent: failed to read embedded schema: %v", err))
	}

	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	if err := compiler.AddResource("intent.v1.schema.json", bytes.NewReader(data)); err != nil {
		panic(fmt.Sprintf("intent: failed to add schema resource: %v", err))
	}
	compiledSchema, err = compiler.Compile("intent.v1.schema.json")
	if err != nil {
		panic(fmt.Sprintf("intent: failed to compile schema: %v", err))
	}
}

// Validate validates an Intent against the canonical JSON Schema.
// Returns a descriptive error that can be relayed back to the user.
func Validate(intent *Intent) error {
	raw, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("intent: marshal failed: %w", err)
	}

	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("intent: unmarshal to interface failed: %w", err)
	}

	if err := compiledSchema.Validate(v); err != nil {
		return fmt.Errorf("intent validation failed: %w", err)
	}
	return nil
}

