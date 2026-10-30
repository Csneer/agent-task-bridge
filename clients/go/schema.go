package bridgeclient

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.json
var schemaJSON []byte
var schemaOnce sync.Once
var definitions map[string]*jsonschema.Schema
var schemaError error

func Validate(definition string, value any) error {
	schemaOnce.Do(func() {
		var document map[string]any
		if e := json.Unmarshal(schemaJSON, &document); e != nil {
			schemaError = e
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		compiler.AssertFormat()
		compiler.RegisterFormat(&jsonschema.Format{Name: "date-time", Validate: func(v any) error {
			s, ok := v.(string)
			if !ok {
				return nil
			}
			t, e := time.Parse("2006-01-02T15:04:05.000Z", s)
			if e != nil || t.Year() == 0 || t.Format("2006-01-02T15:04:05.000Z") != s {
				return errors.New("invalid timestamp")
			}
			return nil
		}})
		compiler.RegisterFormat(&jsonschema.Format{Name: "uri", Validate: func(v any) error {
			s, ok := v.(string)
			if !ok {
				return nil
			}
			colon := strings.IndexByte(s, 58)
			if colon < 1 {
				return errors.New("absolute URI required")
			}
			rest := s[colon+1:]
			if strings.HasPrefix(rest, "//[") {
				end := strings.IndexByte(rest, 93)
				if end < 0 {
					return errors.New("invalid address")
				}
				host := rest[3:end]
				if !strings.HasPrefix(strings.ToLower(host), "v") {
					ip, e := netip.ParseAddr(host)
					if e != nil || !ip.Is6() {
						return errors.New("invalid IPv6")
					}
				}
			}
			return nil
		}})
		id := document["$id"].(string)
		if e := compiler.AddResource(id, document); e != nil {
			schemaError = e
			return
		}
		definitions = map[string]*jsonschema.Schema{}
		for name := range document["$defs"].(map[string]any) {
			s, e := compiler.Compile(id + "#/$defs/" + name)
			if e != nil {
				schemaError = e
				return
			}
			definitions[name] = s
		}
	})
	if schemaError != nil {
		return schemaError
	}
	s, ok := definitions[definition]
	if !ok {
		return errors.New("unknown schema definition")
	}
	return s.Validate(value)
}
