package domain

import (
	"strings"
	"testing"
)

const valid = `{"event_id":"ABCDEFAB-0000-0000-0000-000000000001","sale_reference":"A1_1","currency":"AUD","amount_minor":1000}`

func TestDecodeCanonicalAndBounds(t *testing.T) {
	p, err := Decode(strings.NewReader(valid))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"event_id":"abcdefab-0000-0000-0000-000000000001","sale_reference":"A1_1","currency":"AUD","amount_minor":1000}`
	if string(p.Bytes) != want || len(p.Hash) != 64 {
		t.Fatalf("canonical=%s hash=%s", p.Bytes, p.Hash)
	}
	reordered := `{ "currency":"AUD", "amount_minor":1000,"sale_reference":"A1_1","event_id":"abcdefab-0000-0000-0000-000000000001" }`
	other, err := Decode(strings.NewReader(reordered))
	if err != nil || other.Hash != p.Hash {
		t.Fatalf("equivalent identity changed: %v", err)
	}
	for name, raw := range map[string]string{
		"unknown":   strings.Replace(valid, `"currency"`, `"extra":1,"currency"`, 1),
		"duplicate": strings.Replace(valid, `"currency"`, `"currency":"USD","currency"`, 1),
		"missing":   strings.Replace(valid, `"amount_minor":1000`, `"unused":1000`, 1),
		"zero":      strings.Replace(valid, `1000`, `0`, 1), "negative": strings.Replace(valid, `1000`, `-1`, 1),
		"large": strings.Replace(valid, `1000`, `100000001`, 1), "overflow": strings.Replace(valid, `1000`, `9223372036854775808`, 1),
		"fraction": strings.Replace(valid, `1000`, `1.5`, 1), "exponent": strings.Replace(valid, `1000`, `1e3`, 1),
		"currency": strings.Replace(valid, `AUD`, `USD`, 1), "uuid": strings.Replace(valid, `ABCDEFAB`, `not-uuid`, 1),
		"sale":      strings.Replace(valid, `A1_1`, strings.Repeat("x", 65), 1),
		"salechars": strings.Replace(valid, `A1_1`, `private sale`, 1), "empty": strings.Replace(valid, `A1_1`, ``, 1),
		"trailing": valid + ` {}`, "oversize": strings.Repeat(" ", 4097), "null": `null`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(raw)); err == nil {
				t.Fatal("invalid event accepted")
			}
		})
	}
	for _, amount := range []string{"1", "100000000"} {
		if _, err := Decode(strings.NewReader(strings.Replace(valid, "1000", amount, 1))); err != nil {
			t.Fatal(err)
		}
	}
}
