package config

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Load decodes one pier.yaml document and rejects unknown fields.
func Load(path string) (Config, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)

	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
		return Config{}, fmt.Errorf("decode config: multiple YAML documents are not supported")
	}
	if err := validatePublicScalars(contents); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func validatePublicScalars(contents []byte) error {
	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	return walkPublicScalars(&document)
}

func walkPublicScalars(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode {
		return walkPublicScalars(node.Alias)
	}
	if node.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			if key.Value == "public" && (value.Kind != yaml.ScalarNode || value.Tag != "!!bool") {
				return fmt.Errorf("decode config: line %d: public must be a boolean", value.Line)
			}
			if err := walkPublicScalars(value); err != nil {
				return err
			}
		}
		return nil
	}
	for _, child := range node.Content {
		if err := walkPublicScalars(child); err != nil {
			return err
		}
	}
	return nil
}
