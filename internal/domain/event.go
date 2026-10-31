package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

type Event struct {
	EventID       string `json:"event_id"`
	SaleReference string `json:"sale_reference"`
	Currency      string `json:"currency"`
	AmountMinor   int64  `json:"amount_minor"`
}
type Payload struct {
	Event Event
	Bytes []byte
	Hash  string
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var salePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func Decode(r io.Reader) (Payload, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 4097))
	if err != nil || len(raw) > 4096 {
		return Payload{}, errors.New("event exceeds 4 KiB or cannot be read")
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return Payload{}, errors.New("event must be an object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return Payload{}, errors.New("invalid object")
		}
		key, ok := token.(string)
		if !ok {
			return Payload{}, errors.New("invalid field")
		}
		if _, exists := fields[key]; exists {
			return Payload{}, errors.New("duplicate field")
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return Payload{}, errors.New("invalid value")
		}
		fields[key] = value
	}
	if _, err = d.Token(); err != nil {
		return Payload{}, errors.New("invalid object end")
	}
	var trailing any
	if err = d.Decode(&trailing); err != io.EOF {
		return Payload{}, errors.New("trailing JSON")
	}
	if len(fields) != 4 {
		return Payload{}, errors.New("expected four event fields")
	}
	var e Event
	for key, target := range map[string]any{"event_id": &e.EventID, "sale_reference": &e.SaleReference, "currency": &e.Currency, "amount_minor": &e.AmountMinor} {
		value, ok := fields[key]
		if !ok || string(value) == "null" {
			return Payload{}, fmt.Errorf("missing %s", key)
		}
		if err = json.Unmarshal(value, target); err != nil {
			return Payload{}, fmt.Errorf("invalid %s", key)
		}
	}
	if !uuidPattern.MatchString(e.EventID) || !salePattern.MatchString(e.SaleReference) || e.Currency != "AUD" || e.AmountMinor < 1 || e.AmountMinor > 100000000 {
		return Payload{}, errors.New("invalid event identity or values")
	}
	e.EventID = strings.ToLower(e.EventID)
	canonical, _ := json.Marshal(e)
	hash := sha256.Sum256(canonical)
	return Payload{e, canonical, hex.EncodeToString(hash[:])}, nil
}
