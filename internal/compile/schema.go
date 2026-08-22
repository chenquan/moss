package compile

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed schemas/*.json
var schemaFiles embed.FS

var (
	schemaOnce sync.Once
	schemas    map[string]*jsonschema.Schema
	schemaErr  error
)

func initSchemas() {
	schemas = make(map[string]*jsonschema.Schema)
	for _, stage := range []string{"extract", "classify", "write", "multi-extract", "multi-write"} {
		name := "schemas/" + stage + ".json"
		contents, err := schemaFiles.ReadFile(name)
		if err != nil {
			schemaErr = err
			return
		}
		sch, err := jsonschema.CompileString(name, string(contents))
		if err != nil {
			schemaErr = err
			return
		}
		schemas[stage] = sch
	}
}

func SchemaBytes(stage string) ([]byte, error) {
	return schemaFiles.ReadFile("schemas/" + stage + ".json")
}

func ValidateMulti(stage string, contents []byte) error {
	schemaOnce.Do(initSchemas)
	if schemaErr != nil {
		return schemaErr
	}
	name := "multi-" + stage
	sch, ok := schemas[name]
	if !ok {
		return fmt.Errorf("unsupported multi-source compile stage %q", stage)
	}
	var value any
	if err := json.Unmarshal(contents, &value); err != nil {
		return err
	}
	return sch.Validate(value)
}

func Validate(stage string, contents []byte) error {
	schemaOnce.Do(initSchemas)
	if schemaErr != nil {
		return schemaErr
	}
	sch, ok := schemas[stage]
	if !ok {
		return fmt.Errorf("unsupported compile stage %q", stage)
	}
	var value any
	if err := json.Unmarshal(contents, &value); err != nil {
		return err
	}
	return sch.Validate(value)
}
