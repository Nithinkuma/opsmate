// Package verbs loads and validates verb parameter schemas from a directory.
// Verb schemas are the contract for what parameters each action accepts.
package verbs

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed schemas/*.json
var seedSchemas embed.FS

// Registry holds compiled JSON schemas for every known verb.
type Registry struct {
	schemas map[string]*jsonschema.Schema
}

// LoadSeed returns a Registry populated from the embedded seed schemas.
func LoadSeed() (*Registry, error) {
	return LoadFS(seedSchemas, "schemas")
}

// LoadDir returns a Registry populated from JSON files in a directory path.
func LoadDir(dir string) (*Registry, error) {
	return LoadFS(os.DirFS(dir), ".")
}

// LoadFS builds a Registry from an fs.FS rooted at subdir.
func LoadFS(fsys fs.FS, subdir string) (*Registry, error) {
	r := &Registry{schemas: make(map[string]*jsonschema.Schema)}

	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020

	var files []string
	err := fs.WalkDir(fsys, subdir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".schema.json") {
			return nil
		}
		files = append(files, path)
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("verbs: read %s: %w", path, err)
		}
		id := schemaID(path)
		return compiler.AddResource(id, bytes.NewReader(data))
	})
	if err != nil {
		return nil, err
	}

	for _, path := range files {
		id := schemaID(path)
		sc, err := compiler.Compile(id)
		if err != nil {
			return nil, fmt.Errorf("verbs: compile %s: %w", id, err)
		}
		verb := verbFromPath(path)
		r.schemas[verb] = sc
	}
	return r, nil
}

// Validate checks that params satisfies the schema for the given verb.
func (r *Registry) Validate(verb string, params map[string]interface{}) error {
	sc, ok := r.schemas[verb]
	if !ok {
		return fmt.Errorf("unknown verb %q — not in verb registry", verb)
	}
	if err := sc.Validate(params); err != nil {
		return fmt.Errorf("parameter validation for verb %q failed: %w", verb, err)
	}
	return nil
}

// KnownVerbs returns the list of registered verb names.
func (r *Registry) KnownVerbs() []string {
	verbs := make([]string, 0, len(r.schemas))
	for v := range r.schemas {
		verbs = append(verbs, v)
	}
	return verbs
}

// Has reports whether verb is registered.
func (r *Registry) Has(verb string) bool {
	_, ok := r.schemas[verb]
	return ok
}

func verbFromPath(path string) string {
	base := path[strings.LastIndex(path, "/")+1:]
	return strings.TrimSuffix(base, ".schema.json")
}

func schemaID(path string) string {
	return "file:///" + strings.TrimPrefix(path, "/")
}
