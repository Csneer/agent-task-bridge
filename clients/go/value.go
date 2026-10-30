// Package bridgeclient implements the public M1 protocol independently of the daemon.
package bridgeclient

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const Version = "bridge-m1-1"

var errJSON = errors.New("invalid M1 JSON")

// Parse rejects lossy JSON before decoding. Numbers remain exact json.Number values.
func Parse(raw []byte) (any, error) {
	if !utf8.Valid(raw) {
		return nil, errJSON
	}
	// encoding/json substitutes isolated surrogates; check every escaped code unit first.
	in := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == 34 {
			in = !in
			continue
		}
		if !in || raw[i] != 92 {
			continue
		}
		i++
		if i >= len(raw) {
			return nil, errJSON
		}
		if raw[i] != 117 {
			continue
		}
		if i+4 >= len(raw) {
			return nil, errJSON
		}
		n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return nil, errJSON
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return nil, errJSON
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != 92 || raw[i+2] != 117 {
				return nil, errJSON
			}
			low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return nil, errJSON
			}
			i += 6
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var read func() (any, error)
	read = func() (any, error) {
		token, e := d.Token()
		if e != nil {
			return nil, errJSON
		}
		switch v := token.(type) {
		case json.Delim:
			switch v {
			case 123:
				m := map[string]any{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return nil, errJSON
					}
					key, ok := k.(string)
					if !ok {
						return nil, errJSON
					}
					if _, ok = m[key]; ok {
						return nil, errJSON
					}
					value, e := read()
					if e != nil {
						return nil, e
					}
					m[key] = value
				}
				end, e := d.Token()
				if e != nil || end != json.Delim(125) {
					return nil, errJSON
				}
				return m, nil
			case 91:
				a := []any{}
				for d.More() {
					v, e := read()
					if e != nil {
						return nil, e
					}
					a = append(a, v)
				}
				end, e := d.Token()
				if e != nil || end != json.Delim(93) {
					return nil, errJSON
				}
				return a, nil
			}
			return nil, errJSON
		case json.Number:
			return exactNumber(string(v))
		default:
			return v, nil
		}
	}
	value, e := read()
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, errJSON
	}
	return value, nil
}

var numberSyntax = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

func exactNumber(s string) (json.Number, error) {
	if !numberSyntax.MatchString(s) {
		return "", errJSON
	}
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign = "-"
		s = s[1:]
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == 101 || r == 69 })
	mantissa := parts[0]
	exponent := int64(0)
	if len(parts) == 2 {
		n := new(big.Int)
		if _, ok := n.SetString(parts[1], 10); !ok {
			return "", errJSON
		}
		if n.IsInt64() {
			exponent = n.Int64()
		} else if n.Sign() > 0 {
			exponent = 1 << 60
		} else {
			exponent = -(1 << 60)
		}
	}
	fraction := int64(0)
	if dot := strings.IndexByte(mantissa, 46); dot >= 0 {
		fraction = int64(len(mantissa) - dot - 1)
		mantissa = mantissa[:dot] + mantissa[dot+1:]
	}
	digits := strings.TrimLeft(mantissa, "0")
	if digits == "" {
		return "0", nil
	}
	if exponent > 1000000 || exponent < -1000000 {
		return "", errJSON
	}
	shift := exponent - fraction
	if shift < 0 {
		cut := -shift
		if cut >= int64(len(digits)) {
			return "", errJSON
		}
		for _, ch := range digits[len(digits)-int(cut):] {
			if ch != 48 {
				return "", errJSON
			}
		}
		digits = digits[:len(digits)-int(cut)]
	} else {
		if int64(len(digits))+shift > 16 {
			return "", errJSON
		}
		digits += strings.Repeat("0", int(shift))
	}
	if len(digits) > 16 || (len(digits) == 16 && digits > "9007199254740991") {
		return "", errJSON
	}
	return json.Number(sign + digits), nil
}
func quote(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", errJSON
	}
	var b strings.Builder
	b.WriteByte(34)
	for _, r := range s {
		switch r {
		case 34, 92:
			b.WriteByte(92)
			b.WriteRune(r)
		case 8:
			b.WriteString(`\b`)
		case 9:
			b.WriteString(`\t`)
		case 10:
			b.WriteString(`\n`)
		case 12:
			b.WriteString(`\f`)
		case 13:
			b.WriteString(`\r`)
		default:
			if r < 32 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte(34)
	return b.String(), nil
}

// Canonical accepts parsed JSON values; typed callers should encode then Parse.
func Canonical(value any) ([]byte, error) {
	var emit func(any) (string, error)
	emit = func(v any) (string, error) {
		switch x := v.(type) {
		case nil:
			return "null", nil
		case bool:
			if x {
				return "true", nil
			}
			return "false", nil
		case string:
			return quote(x)
		case json.Number:
			n, e := exactNumber(string(x))
			return string(n), e
		case int:
			return emit(json.Number(strconv.Itoa(x)))
		case int64:
			return emit(json.Number(strconv.FormatInt(x, 10)))
		case []any:
			parts := make([]string, len(x))
			for i, v := range x {
				s, e := emit(v)
				if e != nil {
					return "", e
				}
				parts[i] = s
			}
			return "[" + strings.Join(parts, ",") + "]", nil
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool {
				a, b := utf16.Encode([]rune(keys[i])), utf16.Encode([]rune(keys[j]))
				for k := 0; k < len(a) && k < len(b); k++ {
					if a[k] != b[k] {
						return a[k] < b[k]
					}
				}
				return len(a) < len(b)
			})
			parts := make([]string, 0, len(x))
			for _, k := range keys {
				key, e := quote(k)
				if e != nil {
					return "", e
				}
				val, e := emit(x[k])
				if e != nil {
					return "", e
				}
				parts = append(parts, key+":"+val)
			}
			return "{" + strings.Join(parts, ",") + "}", nil
		default:
			return "", fmt.Errorf("unsupported JSON value %T", v)
		}
	}
	s, e := emit(value)
	return []byte(s), e
}
func RequestHash(method string, params map[string]any) (string, error) {
	p := map[string]any{}
	for k, v := range params {
		if k != "idempotency_key" {
			p[k] = v
		}
	}
	raw, e := Canonical(map[string]any{"protocol_version": Version, "method": method, "params": p})
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
