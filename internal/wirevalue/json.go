// Package wirevalue implements the M1 safe-integer JSON and canonicalization rules.
package wirevalue

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	protocol "agent-task-bridge/protocol/m1"
)

const MaxInteger int64 = 9007199254740991

var numberSyntax = regexp.MustCompile(`^(-?)(0|[1-9][0-9]*)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$`)

// Parse preserves JSON values without floating-point rounding. Every number is
// returned as a normalized integer json.Number. Duplicate keys, invalid Unicode,
// fractions, unsafe integers, and trailing values are rejected.
func Parse(raw []byte) (any, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("JSON contains invalid UTF-8")
	}
	if err := checkSurrogates(raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func readValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case json.Number:
		return normalizeNumber(string(value))
	case json.Delim:
		switch value {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("object key is not a string")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("duplicate JSON object key")
				}
				item, err := readValue(decoder)
				if err != nil {
					return nil, err
				}
				object[key] = item
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("invalid JSON object ending")
			}
			return object, nil
		case '[':
			array := make([]any, 0)
			for decoder.More() {
				item, err := readValue(decoder)
				if err != nil {
					return nil, err
				}
				array = append(array, item)
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("invalid JSON array ending")
			}
			return array, nil
		default:
			return nil, fmt.Errorf("unexpected JSON delimiter")
		}
	default:
		return value, nil
	}
}

// encoding/json replaces isolated UTF-16 escape surrogates with U+FFFD. Reject
// those escapes before decoding, while leaving valid escaped backslashes alone.
func checkSurrogates(raw []byte) error {
	inside := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inside = !inside
			continue
		}
		if !inside || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) || raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return fmt.Errorf("incomplete Unicode escape")
		}
		code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return fmt.Errorf("invalid Unicode escape")
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return fmt.Errorf("isolated low surrogate")
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return fmt.Errorf("isolated high surrogate")
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return fmt.Errorf("invalid surrogate pair")
			}
			i += 6
		}
	}
	return nil
}

// normalizeNumber computes the exact decimal value using digit positions. It
// never allocates an exponent-sized big integer and never rounds through float64.
func normalizeNumber(raw string) (json.Number, error) {
	parts := numberSyntax.FindStringSubmatch(raw)
	if parts == nil {
		return "", fmt.Errorf("invalid JSON number")
	}
	digits := strings.TrimLeft(parts[2]+parts[3], "0")
	if digits == "" {
		return json.Number("0"), nil
	}
	exponent := 0
	if parts[4] != "" {
		var err error
		exponent, err = strconv.Atoi(parts[4])
		if err != nil || exponent > len(raw)+16 || exponent < -len(raw)-16 {
			return "", fmt.Errorf("number is not a safe integer")
		}
	}
	shift := exponent - len(parts[3])
	trimmed := strings.TrimRight(digits, "0")
	shift += len(digits) - len(trimmed)
	digits = trimmed
	if shift < 0 || len(digits)+shift > 16 {
		return "", fmt.Errorf("number is not a safe integer")
	}
	digits += strings.Repeat("0", shift)
	n, err := strconv.ParseInt(parts[1]+digits, 10, 64)
	if err != nil || n < -MaxInteger || n > MaxInteger {
		return "", fmt.Errorf("number is not a safe integer")
	}
	return json.Number(strconv.FormatInt(n, 10)), nil
}

// Canonical serializes the protocol's safe-integer subset of RFC 8785 JCS.
// Values may use JSON-native maps/slices or typed Go JSON representations.
// Untrusted wire bytes must enter through Parse before reaching this API.
func Canonical(value any) ([]byte, error) {
	var out bytes.Buffer
	if err := writeCanonical(&out, value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeCanonical(out *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(v))
	case string:
		return writeString(out, v)
	case json.Number:
		n, err := normalizeNumber(string(v))
		if err != nil {
			return err
		}
		out.WriteString(string(n))
	case int:
		return writeCanonical(out, json.Number(strconv.FormatInt(int64(v), 10)))
	case int8:
		return writeCanonical(out, int64(v))
	case int16:
		return writeCanonical(out, int64(v))
	case int32:
		return writeCanonical(out, int64(v))
	case int64:
		return writeCanonical(out, json.Number(strconv.FormatInt(v, 10)))
	case uint:
		return writeCanonical(out, uint64(v))
	case uint8:
		return writeCanonical(out, uint64(v))
	case uint16:
		return writeCanonical(out, uint64(v))
	case uint32:
		return writeCanonical(out, uint64(v))
	case uint64:
		return writeCanonical(out, json.Number(strconv.FormatUint(v, 10)))
	case float32:
		return writeCanonical(out, float64(v))
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v || math.Abs(v) > float64(MaxInteger) {
			return fmt.Errorf("number is not a safe integer")
		}
		return writeCanonical(out, int64(v))
	case []any:
		out.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := writeCanonical(out, item); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			if !utf8.ValidString(key) {
				return fmt.Errorf("invalid Unicode object key")
			}
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := writeString(out, key); err != nil {
				return err
			}
			out.WriteByte(':')
			if err := writeCanonical(out, v[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		// Preserve the standard encoding/json contract for struct tags, typed
		// slices/maps, pointers, and Marshaler implementations, then enforce the
		// exact wire profile. Check original strings because encoding/json would
		// otherwise silently replace malformed UTF-8 with U+FFFD.
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if err := checkTypedStrings(reflect.ValueOf(value), 0); err != nil {
			return err
		}
		normalized, err := Parse(raw)
		if err != nil {
			return err
		}
		return writeCanonical(out, normalized)
	}
	return nil
}

func checkTypedStrings(value reflect.Value, depth int) error {
	if !value.IsValid() {
		return nil
	}
	if depth > 1000 {
		return fmt.Errorf("typed JSON value is too deeply nested")
	}
	switch value.Kind() {
	case reflect.String:
		if !utf8.ValidString(value.String()) {
			return fmt.Errorf("invalid Unicode string")
		}
	case reflect.Interface, reflect.Pointer:
		if !value.IsNil() {
			return checkTypedStrings(value.Elem(), depth+1)
		}
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			if err := checkTypedStrings(iterator.Key(), depth+1); err != nil {
				return err
			}
			if err := checkTypedStrings(iterator.Value(), depth+1); err != nil {
				return err
			}
		}
	case reflect.Array, reflect.Slice:
		for i := 0; i < value.Len(); i++ {
			if err := checkTypedStrings(value.Index(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.PkgPath == "" && field.Tag.Get("json") != "-" {
				if err := checkTypedStrings(value.Field(i), depth+1); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func utf16Less(a, b string) bool {
	x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}

func writeString(out *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("invalid Unicode string")
	}
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\b':
			out.WriteString(`\b`)
		case '\t':
			out.WriteString(`\t`)
		case '\n':
			out.WriteString(`\n`)
		case '\f':
			out.WriteString(`\f`)
		case '\r':
			out.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return nil
}

// RequestHash excludes only the idempotency key, not any semantic parameter.
func RequestHash(method string, params map[string]any) (string, error) {
	copyParams := make(map[string]any, len(params))
	for key, value := range params {
		if key != "idempotency_key" {
			copyParams[key] = value
		}
	}
	raw, err := Canonical(map[string]any{"protocol_version": protocol.Version, "method": method, "params": copyParams})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
