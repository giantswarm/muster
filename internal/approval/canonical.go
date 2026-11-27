package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// MaxArgumentsSize caps the canonical arguments of a call that asks: a larger
// call is refused rather than stored and shown.
const MaxArgumentsSize = 64 << 10

// Canonical returns the RFC 8785 (JSON Canonicalization Scheme) form of the
// decoded arguments muster dispatches, and its SHA-256 in hex. What is bound,
// shown and hashed is this one string. Objects are sorted by the UTF-16 code
// units of their keys, numbers are written as ECMAScript does, strings with
// the minimal escaping; NaN and infinities are refused.
func Canonical(args map[string]any) (canonical, hash string, err error) {
	var b strings.Builder
	if args == nil {
		args = map[string]any{}
	}
	if err := writeCanonical(&b, args); err != nil {
		return "", "", err
	}
	canonical = b.String()
	if len(canonical) > MaxArgumentsSize {
		return "", "", fmt.Errorf("the arguments are %d bytes, more than the %d an approval can show", len(canonical), MaxArgumentsSize)
	}
	return canonical, Hash(canonical), nil
}

// Hash returns the SHA-256 of a canonical form, in hex.
func Hash(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// DecodeCanonical decodes stored canonical arguments into the values muster
// dispatches: numbers become float64, as a client's arguments do.
func DecodeCanonical(canonical string) (map[string]any, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(canonical), &args); err != nil {
		return nil, fmt.Errorf("decode canonical arguments: %w", err)
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

func writeCanonical(b *strings.Builder, value any) error {
	switch v := value.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case string:
		writeString(b, v)
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return fmt.Errorf("number %q: %w", v, err)
		}
		return writeNumber(b, f)
	case float64:
		return writeNumber(b, v)
	case float32:
		return writeNumber(b, float64(v))
	case int:
		return writeNumber(b, float64(v))
	case int64:
		return writeNumber(b, float64(v))
	case int32:
		return writeNumber(b, float64(v))
	case map[string]any:
		return writeObject(b, v)
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	default:
		// Any other Go value (a typed slice or map from an internal caller)
		// is normalised through encoding/json first.
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("value of type %s: %w", reflect.TypeOf(v), err)
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return err
		}
		return writeCanonical(b, decoded)
	}
	return nil
}

func writeObject(b *strings.Builder, object map[string]any) error {
	keys := make([]string, 0, len(object))
	for k := range object {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, compareUTF16)
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		writeString(b, k)
		b.WriteByte(':')
		if err := writeCanonical(b, object[k]); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

// compareUTF16 orders strings by their UTF-16 code units (RFC 8785 §3.2.3).
func compareUTF16(a, b string) int {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	return slices.Compare(ua, ub)
}

// writeString writes a JSON string with ECMAScript's escaping: the quote,
// the backslash and control characters only.
func writeString(b *strings.Builder, s string) {
	const hexDigits = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte(hexDigits[r>>4])
			b.WriteByte(hexDigits[r&0xf])
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

// writeNumber writes an IEEE 754 double as ECMAScript's Number.toString does
// (RFC 8785 §3.2.2.3).
func writeNumber(b *strings.Builder, f float64) error {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("number %v cannot be written as JSON", f)
	}
	if f == 0 {
		b.WriteByte('0')
		return nil
	}
	abs := math.Abs(f)
	if abs >= 1e-6 && abs < 1e21 {
		b.WriteString(strconv.FormatFloat(f, 'f', -1, 64))
		return nil
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, exponent, _ := strings.Cut(s, "e")
	sign := exponent[:1]
	digits := strings.TrimLeft(exponent[1:], "0")
	if digits == "" {
		digits = "0"
	}
	b.WriteString(mantissa)
	b.WriteByte('e')
	b.WriteString(sign)
	b.WriteString(digits)
	return nil
}
