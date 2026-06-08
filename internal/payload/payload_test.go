package payload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseInlineJSON(t *testing.T) {
	doc, err := Parse(`{"product_id_or_name":"demo"}`, strings.NewReader(""))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if doc["product_id_or_name"] != "demo" {
		t.Fatalf("unexpected payload: %#v", doc)
	}
}

func TestParseFileJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(path, []byte(`{"workspace_id":"w1"}`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	doc, err := Parse("@"+path, strings.NewReader(""))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if doc["workspace_id"] != "w1" {
		t.Fatalf("unexpected payload: %#v", doc)
	}
}

func TestParseStdinJSON(t *testing.T) {
	doc, err := Parse("-", strings.NewReader(`{"ok":true}`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if doc["ok"] != true {
		t.Fatalf("unexpected payload: %#v", doc)
	}
}

func TestParseInvalidJSON(t *testing.T) {
	_, err := Parse("{", strings.NewReader(""))
	if err == nil {
		t.Fatal("expected invalid JSON error")
	}
}
