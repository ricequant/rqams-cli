package output

import (
	"encoding/json"
	"io"
)

// ErrorInfo describes a machine-readable CLI failure.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Envelope is the stable JSON response contract for all CLI commands.
type Envelope struct {
	OK      bool           `json:"ok"`
	Command string         `json:"command"`
	Data    any            `json:"data,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
	Error   *ErrorInfo     `json:"error,omitempty"`
}

// Write emits one envelope as formatted JSON.
func Write(w io.Writer, envelope Envelope) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(envelope)
}

// WriteNDJSON emits each item as one compact JSON object per line.
func WriteNDJSON(w io.Writer, items []any) error {
	encoder := json.NewEncoder(w)
	for _, item := range items {
		if err := encoder.Encode(item); err != nil {
			return err
		}
	}
	return nil
}

// Success builds a successful envelope.
func Success(command string, data any, meta map[string]any) Envelope {
	return Envelope{
		OK:      true,
		Command: command,
		Data:    data,
		Meta:    meta,
	}
}

// Failure builds a failed envelope.
func Failure(command string, code string, message string) Envelope {
	return Envelope{
		OK:      false,
		Command: command,
		Error: &ErrorInfo{
			Code:    code,
			Message: message,
		},
	}
}
