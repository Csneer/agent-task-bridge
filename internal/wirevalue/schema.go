package wirevalue

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"time"

	protocol "agent-task-bridge/protocol/m1"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	schemaOnce sync.Once
	schemas    map[string]*jsonschema.Schema
	schemaErr  error
)

// Validate validates a JSON-native value against one named frozen $defs entry.
// It enforces profile-wide Unicode and safe-integer rules before JSON Schema.
func Validate(definition string, value any) error {
	schemaOnce.Do(compileSchemas)
	if schemaErr != nil {
		return schemaErr
	}
	schema, exists := schemas[definition]
	if !exists {
		return fmt.Errorf("unknown protocol definition %q", definition)
	}
	raw, err := Canonical(value)
	if err != nil {
		return err
	}
	normalized, err := Parse(raw)
	if err != nil {
		return err
	}
	return schema.Validate(normalized)
}

func compileSchemas() {
	parsed, err := Parse(protocol.Schema)
	if err != nil {
		schemaErr = fmt.Errorf("embedded schema: %w", err)
		return
	}
	document := parsed.(map[string]any)
	definitions := document["$defs"].(map[string]any)
	uriDefinition := definitions["ImmutableRef"].(map[string]any)["properties"].(map[string]any)["uri"].(map[string]any)
	uriPattern, err := regexp.Compile(uriDefinition["pattern"].(string))
	if err != nil {
		schemaErr = fmt.Errorf("embedded URI pattern: %w", err)
		return
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.RegisterFormat(&jsonschema.Format{Name: "date-time", Validate: validateTimestamp})
	compiler.RegisterFormat(&jsonschema.Format{Name: "uri", Validate: func(value any) error {
		return validateURI(uriPattern, value)
	}})
	identifier := document["$id"].(string)
	if err := compiler.AddResource(identifier, document); err != nil {
		schemaErr = err
		return
	}
	schemas = make(map[string]*jsonschema.Schema, len(definitions))
	for name := range definitions {
		schema, err := compiler.Compile(identifier + "#/$defs/" + name)
		if err != nil {
			schemaErr = fmt.Errorf("compile protocol definition %s: %w", name, err)
			return
		}
		schemas[name] = schema
	}
}

func validateTimestamp(value any) error {
	text, ok := value.(string)
	if !ok {
		return nil
	}
	if len(text) != len("2006-01-02T15:04:05.000Z") {
		return fmt.Errorf("timestamp must have millisecond UTC precision")
	}
	parsed, err := time.Parse("2006-01-02T15:04:05.000Z", text)
	if err != nil || parsed.Year() < 1 || parsed.Format("2006-01-02T15:04:05.000Z") != text {
		return fmt.Errorf("invalid millisecond UTC timestamp")
	}
	return nil
}

func validateURI(pattern *regexp.Regexp, value any) error {
	text, ok := value.(string)
	if !ok {
		return nil
	}
	if !pattern.MatchString(text) {
		return fmt.Errorf("invalid resource URI")
	}
	// The frozen URI grammar already rejects userinfo and malformed escapes.
	// Bracketed IPv6 still needs semantic validation; IPvFuture's v/V is case
	// insensitive and is intentionally not delegated to net/url behavior.
	_, rest, _ := strings.Cut(text, ":")
	if strings.HasPrefix(rest, "//[") {
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return fmt.Errorf("invalid IP literal")
		}
		host := rest[3:end]
		if !strings.HasPrefix(host, "v") && !strings.HasPrefix(host, "V") {
			address, err := netip.ParseAddr(host)
			if err != nil || !address.Is6() {
				return fmt.Errorf("invalid IPv6 resource host")
			}
		}
	}
	return nil
}
