package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// AddService inserts a service into pier.yaml, preserving comments when possible.
func AddService(path, name string, service Service) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("service name is required")
	}

	raw, err := Load(path)
	if err != nil {
		return err
	}
	if _, exists := raw.Services[name]; exists {
		return fmt.Errorf("service %q already exists", name)
	}
	if raw.Services == nil {
		raw.Services = map[string]Service{}
	}
	raw.Services[name] = service
	normalized, err := Normalize(raw)
	if err != nil {
		return err
	}
	if errs := Validate(normalized); len(errs) > 0 {
		return fmt.Errorf("%s", errs[0].Error())
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	updated, err := insertServiceYAML(contents, name, service)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, updated)
}

func insertServiceYAML(contents []byte, name string, service Service) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(contents, &doc); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	root := mappingRoot(&doc)
	if root == nil {
		return nil, fmt.Errorf("decode config: expected a YAML mapping")
	}
	services := mappingValue(root, "services")
	if services == nil || services.Kind == yaml.ScalarNode && (services.Tag == "!!null" || services.Value == "" || services.Value == "null") {
		services = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(root, "services", services)
	}
	if services.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("decode config: services must be a mapping")
	}
	if mappingValue(services, name) != nil {
		return nil, fmt.Errorf("service %q already exists", name)
	}
	appendMapping(services, name, serviceNode(service), len(services.Content) > 0)
	return encodeYAML(&doc)
}

func mappingRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		if doc.Content[0].Kind == yaml.MappingNode {
			return doc.Content[0]
		}
		return nil
	}
	if doc.Kind == yaml.MappingNode {
		return doc
	}
	return nil
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setMappingValue(m *yaml.Node, key string, value *yaml.Node) {
	if existing := mappingValue(m, key); existing != nil {
		*existing = *value
		return
	}
	appendMapping(m, key, value, false)
}

func appendMapping(m *yaml.Node, key string, value *yaml.Node, blankLine bool) {
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	if blankLine {
		keyNode.HeadComment = "\n"
	}
	m.Content = append(m.Content, keyNode, value)
}

func serviceNode(service Service) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	appendMapping(node, "target", scalarNode(service.Target), false)
	path := service.Path
	if path == "" {
		path = "/"
	}
	appendMapping(node, "path", scalarNode(path), false)
	if service.Protocol != "" {
		appendMapping(node, "protocol", scalarNode(service.Protocol), false)
	}
	if service.Public != nil {
		value := boolNode(service.Public.On)
		if service.Public.For > 0 {
			value = scalarNode(service.Public.For.String())
		}
		appendMapping(node, "public", value, false)
	}
	return node
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func boolNode(value bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(value)}
}

func encodeYAML(node *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	return buf.Bytes(), nil
}

func writeFileAtomic(path string, contents []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".pier-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish config: %w", err)
	}
	return nil
}
