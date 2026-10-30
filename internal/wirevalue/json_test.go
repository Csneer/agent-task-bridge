package wirevalue

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	protocol "github.com/Csneer/agent-task-bridge/protocol/m1"
)

type fixtures struct {
	Schema []struct {
		ID         string          `json:"id"`
		Definition string          `json:"definition"`
		Valid      bool            `json:"valid"`
		Value      json.RawMessage `json:"value"`
	} `json:"schema_cases"`
	Hashes []struct {
		ID      string `json:"id"`
		Request struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		} `json:"request"`
		Canonical string `json:"canonical"`
		SHA256    string `json:"sha256"`
	} `json:"hash_cases"`
	Parser []struct {
		ID        string `json:"id"`
		JSON      string `json:"json"`
		Valid     bool   `json:"valid"`
		Canonical string `json:"canonical"`
	} `json:"parser_cases"`
}

func loadFixtures(t *testing.T) fixtures {
	t.Helper()
	var fixtures fixtures
	if err := json.Unmarshal(protocol.Golden, &fixtures); err != nil {
		t.Fatal(err)
	}
	return fixtures
}

func TestFrozenSchemaVectors(t *testing.T) {
	fixtures := loadFixtures(t)
	if len(fixtures.Schema) != 92 {
		t.Fatalf("unexpected baseline structural vector count: %d", len(fixtures.Schema))
	}
	for _, test := range fixtures.Schema {
		t.Run(test.ID, func(t *testing.T) {
			value, err := Parse(test.Value)
			if err == nil {
				err = Validate(test.Definition, value)
			}
			if (err == nil) != test.Valid {
				t.Fatalf("want valid=%v, got error=%v", test.Valid, err)
			}
		})
	}
}

func TestFrozenHashVectors(t *testing.T) {
	fixtures := loadFixtures(t)
	if len(fixtures.Hashes) != 7 {
		t.Fatalf("unexpected baseline hash vector count: %d", len(fixtures.Hashes))
	}
	for _, test := range fixtures.Hashes {
		t.Run(test.ID, func(t *testing.T) {
			value, err := Parse(test.Request.Params)
			if err != nil {
				t.Fatal(err)
			}
			params := value.(map[string]any)
			digest, err := RequestHash(test.Request.Method, params)
			if err != nil || digest != test.SHA256 {
				t.Fatalf("digest got %q (%v), want %q", digest, err, test.SHA256)
			}
			delete(params, "idempotency_key")
			canonical, err := Canonical(map[string]any{"protocol_version": protocol.Version, "method": test.Request.Method, "params": params})
			if err != nil || string(canonical) != test.Canonical {
				t.Fatalf("canonical bytes differ: %s (%v)", canonical, err)
			}
		})
	}
}

func TestFrozenParserVectors(t *testing.T) {
	fixtures := loadFixtures(t)
	if len(fixtures.Parser) != 9 {
		t.Fatalf("unexpected baseline parser vector count: %d", len(fixtures.Parser))
	}
	for _, test := range fixtures.Parser {
		t.Run(test.ID, func(t *testing.T) {
			value, err := Parse([]byte(test.JSON))
			if (err == nil) != test.Valid {
				t.Fatalf("want valid=%v, got %v", test.Valid, err)
			}
			if test.Valid {
				raw, err := Canonical(value)
				if err != nil || string(raw) != test.Canonical {
					t.Fatalf("canonical got %q (%v), want %q", raw, err, test.Canonical)
				}
			}
		})
	}
}

func TestStrictParsing(t *testing.T) {
	valid := map[string]string{
		`"\ud83d\ude00"`:        `"😀"`,
		`"\\ud800"`:             `"\\ud800"`,
		`{"x":1.00e2}`:          `{"x":100}`,
		`9007199254740991`:      `9007199254740991`,
		`-9007199254740991`:     `-9007199254740991`,
		`0e9999999999999999999`: `0`,
		`1000e-3`:               `1`,
		`0.0001e4`:              `1`,
		`[{},[],true,null]`:     `[{},[],true,null]`,
	}
	for input, want := range valid {
		value, err := Parse([]byte(input))
		if err != nil {
			t.Errorf("%s: %v", input, err)
			continue
		}
		got, err := Canonical(value)
		if err != nil || string(got) != want {
			t.Errorf("%s: got %s (%v), want %s", input, got, err, want)
		}
	}
	invalid := []string{
		`{"x":1,"\u0078":2}`, `{"n":{"x":1,"x":2}}`,
		`"\ud800x"`, `"\udfff"`, `"\ud800\u0041"`, `"\ud800\ud800"`,
		`"\uZZZZ"`, `"\ud800`, `"` + string([]byte{0xff}) + `"`,
		`1 2`, `{}[]`, ``, `01`, `1.`, `NaN`, `Infinity`,
		`9007199254740992`, `-9007199254740992`, `1e999999999999999999999`,
		`1e-999999999999999999999`, `1.0000000000000001`, `100e-3`,
	}
	for _, input := range invalid {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("accepted invalid input %q", input)
		}
	}
}

func TestCanonicalStringsAndOrdering(t *testing.T) {
	value := map[string]any{"\ue000": 2, "😀": 1, "control": "\x00\b\t\n\f\r\"\\/<>&\u2028\u2029"}
	got, err := Canonical(value)
	want := "{\"control\":\"\\u0000\\b\\t\\n\\f\\r\\\"\\\\/<>&\u2028\u2029\",\"😀\":1,\"\ue000\":2}"
	if err != nil || string(got) != want {
		t.Fatalf("got %q (%v), want %q", got, err, want)
	}
	for _, value := range []any{math.NaN(), math.Inf(1), 0.1, uint64(MaxInteger + 1), string([]byte{0xff}), map[string]any{string([]byte{0xff}): 1}, json.Number("01")} {
		if _, err := Canonical(value); err == nil {
			t.Errorf("accepted invalid native value %#v", value)
		}
	}
}

func TestProfileFormatsAndExtensions(t *testing.T) {
	for _, timestamp := range []string{"0000-01-01T00:00:00.000Z", "2026-02-30T00:00:00.000Z", "2026-10-30T00:00:60.000Z", "2026-10-30T00:00:00.000+00:00"} {
		if err := Validate("Timestamp", timestamp); err == nil {
			t.Errorf("accepted invalid timestamp %s", timestamp)
		}
	}
	for _, uri := range []string{"https://[:::]/", "https://[127.0.0.1]/", "https://u:p@example.com/", "https://example.com/%zz", "https://example.com/中"} {
		ref := map[string]any{"kind": "immutable", "uri": uri, "media_type": "text/plain", "version": "1", "sha256": strings.Repeat("a", 64)}
		if err := Validate("ImmutableRef", ref); err == nil {
			t.Errorf("accepted invalid URI %s", uri)
		}
	}
	if err := Validate("Extensions", map[string]any{"org.example.data": map[string]any{"x": json.Number("1.0000000000000001")}}); err == nil {
		t.Fatal("fraction in extension accepted")
	}
	if err := Validate("missing", nil); err == nil {
		t.Fatal("unknown definition accepted")
	}
}

func TestTypedJSONRepresentations(t *testing.T) {
	type object map[string]any
	type response struct {
		Data object   `json:"data"`
		IDs  []string `json:"ids"`
		Hide string   `json:"-"`
	}
	value := response{Data: object{"count": int64(2)}, IDs: []string{"b", "a"}}
	raw, err := Canonical(value)
	if err != nil || string(raw) != `{"data":{"count":2},"ids":["b","a"]}` {
		t.Fatalf("typed canonical result: %s %v", raw, err)
	}
	if err := Validate("Extensions", object{"org.example.ids": []string{"a"}}); err != nil {
		t.Fatalf("typed validation: %v", err)
	}
	value.IDs[0] = string([]byte{0xff})
	if _, err := Canonical(value); err == nil {
		t.Fatal("typed malformed Unicode silently replaced")
	}
}
