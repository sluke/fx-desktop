package app

import (
	"encoding/json"
	"sort"
	"strings"
)

// presentCredits renders an fx credits JSON document without turning a
// missing or null field into zero. A number that is actually zero stays
// zero. Text that is not JSON is returned unchanged.
func presentCredits(text string, err error) (string, error) {
	if err != nil || strings.TrimSpace(text) == "" {
		return text, err
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var value any
	if dec.Decode(&value) != nil {
		return text, err
	}
	var b strings.Builder
	writeCredit(&b, value, 0)
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.WriteString("Source: fx credits")
	if !strings.Contains(text, "token_pass") && !strings.Contains(text, "token_pass_remaining") {
		b.WriteString("\nToken Pass: 未提供")
	}
	return b.String(), nil
}

func writeCredit(b *strings.Builder, value any, depth int) {
	switch v := value.(type) {
	case nil:
		b.WriteString("未提供")
	case json.Number:
		b.WriteString(v.String())
	case string:
		b.WriteString(v)
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for i, key := range keys {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(strings.Repeat("  ", depth))
			b.WriteString(key)
			b.WriteString(": ")
			writeCredit(b, v[key], depth+1)
		}
	case []any:
		if len(v) == 0 {
			b.WriteString("未提供")
			return
		}
		for i, item := range v {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(strings.Repeat("  ", depth))
			writeCredit(b, item, depth+1)
		}
	default:
		b.WriteString("未提供")
	}
}
