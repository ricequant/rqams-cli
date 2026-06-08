package payload

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var (
	// ErrMissingPayload is returned when --payload is absent.
	ErrMissingPayload = errors.New("missing --payload")
	// ErrEmptyPayload is returned when the payload source has no content.
	ErrEmptyPayload = errors.New("payload is empty")
)

// Parse reads --payload content from inline JSON, @file, or stdin marker "-".
func Parse(value string, stdin io.Reader) (map[string]any, error) {
	if strings.TrimSpace(value) == "" {
		return nil, ErrMissingPayload
	}

	raw, err := readRaw(value, stdin)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, ErrEmptyPayload
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("payload must be valid JSON: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

func readRaw(value string, stdin io.Reader) ([]byte, error) {
	if value == "-" {
		return io.ReadAll(stdin)
	}
	if strings.HasPrefix(value, "@") {
		path := strings.TrimPrefix(value, "@")
		if strings.TrimSpace(path) == "" {
			return nil, errors.New("@ payload path is empty")
		}
		return os.ReadFile(path)
	}
	return []byte(value), nil
}

// String returns a required string field from payload.
func String(doc map[string]any, field string) (string, error) {
	value, ok := doc[field]
	if !ok {
		return "", fmt.Errorf("payload missing required field %q", field)
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("payload field %q must be a non-empty string", field)
	}
	return text, nil
}

// Object returns a required object field from payload.
func Object(doc map[string]any, field string) (map[string]any, error) {
	value, ok := doc[field]
	if !ok {
		return nil, fmt.Errorf("payload missing required field %q", field)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("payload field %q must be an object", field)
	}
	return object, nil
}
