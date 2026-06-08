package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpAndVersionDoNotRequireConfig(t *testing.T) {
	clearRQAMSCEnv(t)
	t.Setenv("RQAMS_CLI_CONFIG", filepath.Join(t.TempDir(), "missing.json"))

	for _, args := range [][]string{
		{},
		{"--help"},
		{"help"},
	} {
		var stdout strings.Builder
		code := Run(args, strings.NewReader(""), &stdout, &strings.Builder{})
		if code != 0 {
			t.Fatalf("Run(%v) returned code %d: %s", args, code, stdout.String())
		}
		if !strings.Contains(stdout.String(), "Usage:") || !strings.Contains(stdout.String(), "schema list") {
			t.Fatalf("unexpected help output for %v: %s", args, stdout.String())
		}
	}

	var stdout strings.Builder
	code := Run([]string{"--version"}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("version returned code %d: %s", code, stdout.String())
	}
	if strings.TrimSpace(stdout.String()) != "rqamsc version 0.0.1" {
		t.Fatalf("unexpected version output: %s", stdout.String())
	}
}

func TestSchemaListDoesNotRequireConfig(t *testing.T) {
	clearRQAMSCEnv(t)
	t.Setenv("RQAMS_CLI_CONFIG", filepath.Join(t.TempDir(), "missing.json"))

	var stdout strings.Builder
	code := Run([]string{"schema", "list"}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("schema list returned code %d: %s", code, stdout.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if envelope["ok"] != true || envelope["command"] != "schema list" {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
	data, ok := envelope["data"].([]any)
	if !ok || len(data) == 0 {
		t.Fatalf("schema list should return command items: %#v", envelope["data"])
	}
	if !strings.Contains(stdout.String(), `"command": "get product-list"`) {
		t.Fatalf("schema list should include product-list: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"supports_ndjson": true`) {
		t.Fatalf("schema list should expose ndjson support: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), `"command": "open auth"`) {
		t.Fatalf("schema list should not expose removed auth alias: %s", stdout.String())
	}
}

func TestSchemaGetReturnsPayloadGuidance(t *testing.T) {
	clearRQAMSCEnv(t)
	t.Setenv("RQAMS_CLI_CONFIG", filepath.Join(t.TempDir(), "missing.json"))

	var stdout strings.Builder
	code := Run(
		[]string{"schema", "get", "--payload", `{"command":"get product-list"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("schema get returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"command": "get product-list"`) {
		t.Fatalf("schema get should include command: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"optional_payload"`) || !strings.Contains(stdout.String(), `"fields"`) {
		t.Fatalf("schema get should include payload guidance: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"parameters"`) || !strings.Contains(stdout.String(), `"returns"`) {
		t.Fatalf("schema get should include field schema and return summary: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "data.products[]") {
		t.Fatalf("schema get should describe product-list return shape: %s", stdout.String())
	}
}

func TestSchemaGetProductExposesSingleProductLocator(t *testing.T) {
	clearRQAMSCEnv(t)
	t.Setenv("RQAMS_CLI_CONFIG", filepath.Join(t.TempDir(), "missing.json"))

	var stdout strings.Builder
	code := Run(
		[]string{"schema", "get", "--payload", `{"command":"get product"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("schema get returned code %d: %s", code, stdout.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("schema data should be an object: %#v", envelope["data"])
	}
	parameters, ok := data["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("schema should include parameters: %#v", data)
	}
	if _, ok := parameters["product_id_or_name"]; !ok {
		t.Fatalf("schema should expose product_id_or_name: %#v", parameters)
	}
	if _, ok := parameters["product_id"]; ok {
		t.Fatalf("schema should not expose product_id separately: %#v", parameters)
	}
}

func TestAuthSingleWordCommand(t *testing.T) {
	clearRQAMSCEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/login" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm returned error: %v", err)
		}
		if r.Form.Get("username") != "demo-user" || r.Form.Get("password") != "demo-pass" {
			t.Fatalf("unexpected login form: %#v", r.Form)
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "demo-sid"})
		_, _ = w.Write([]byte(`{"code":0,"data":{"user_id":"demo-user-id"}}`))
	}))
	defer server.Close()
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"auth", "--payload", `{"base_url":` + quote(server.URL) + `,"username":"demo-user","password":"demo-pass"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("auth returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"command": "auth"`) || !strings.Contains(stdout.String(), `"authenticated": true`) {
		t.Fatalf("unexpected auth output: %s", stdout.String())
	}
	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	var savedConfig map[string]any
	if err := json.Unmarshal(saved, &savedConfig); err != nil {
		t.Fatalf("saved config should be JSON: %v", err)
	}
	if savedConfig["sid"] != "demo-sid" || savedConfig["user_id"] != "demo-user-id" || savedConfig["password"] != "demo-pass" {
		t.Fatalf("config should persist login state: %#v", savedConfig)
	}
}

func TestAuthProfilePersistsIsolatedSession(t *testing.T) {
	clearRQAMSCEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/login" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm returned error: %v", err)
		}
		username := r.Form.Get("username")
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: username + "-sid"})
		_, _ = w.Write([]byte(`{"code":0,"data":{"user_id":"` + username + `-id"}}`))
	}))
	defer server.Close()
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	for _, profile := range []string{"acct-a-w1", "acct-b-w2"} {
		username := strings.Split(profile, "-")[1]
		var stdout strings.Builder
		code := Run(
			[]string{"auth", "--payload", `{"profile":` + quote(profile) + `,"base_url":` + quote(server.URL) + `,"username":` + quote(username) + `,"password":"demo-pass"}`},
			strings.NewReader(""),
			&stdout,
			&strings.Builder{},
		)
		if code != 0 {
			t.Fatalf("auth returned code %d: %s", code, stdout.String())
		}
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	var savedConfig map[string]any
	if err := json.Unmarshal(raw, &savedConfig); err != nil {
		t.Fatalf("saved config should be JSON: %v", err)
	}
	profiles, ok := savedConfig["profiles"].(map[string]any)
	if !ok || len(profiles) != 2 {
		t.Fatalf("config should persist isolated profiles: %#v", savedConfig)
	}
	profileA, ok := profiles["acct-a-w1"].(map[string]any)
	if !ok || profileA["sid"] != "a-sid" || profileA["user_id"] != "a-id" || profileA["password"] != "demo-pass" {
		t.Fatalf("unexpected acct-a-w1 profile: %#v", profileA)
	}
	profileB, ok := profiles["acct-b-w2"].(map[string]any)
	if !ok || profileB["sid"] != "b-sid" || profileB["user_id"] != "b-id" || profileB["password"] != "demo-pass" {
		t.Fatalf("unexpected acct-b-w2 profile: %#v", profileB)
	}
}

func TestSchemaGetRequiresKnownCommand(t *testing.T) {
	clearRQAMSCEnv(t)
	var stdout strings.Builder
	code := Run(
		[]string{"schema", "get", "--payload", `{"command":"open auth"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code == 0 {
		t.Fatalf("expected schema get failure: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"ok": false`) || !strings.Contains(stdout.String(), "unsupported command") {
		t.Fatalf("unexpected schema get failure: %s", stdout.String())
	}
}

func TestGetProductList(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("fields") != "id,name,start_date,label" {
			t.Fatalf("unexpected fields query: %s", r.URL.RawQuery)
		}
		if r.Header.Get("X-AMS-Workspace") != "w1" {
			t.Fatalf("missing workspace header")
		}
		_, _ = w.Write([]byte(`{"products":[{"id":"p1","name":"demo","start_date":"2026-01-01","label":"live","user_id":1}],"permissions":[{"product_id":"p1"}],"total":1}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "product-list", "--payload", "{}"}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if envelope["ok"] != true {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected data: %#v", envelope["data"])
	}
	if _, ok := data["permissions"]; ok {
		t.Fatalf("permissions should be removed from default output: %#v", data)
	}
	if strings.Contains(stdout.String(), `"user_id"`) {
		t.Fatalf("unexpected projected field in default output: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"label": "live"`) {
		t.Fatalf("default output should include product label: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), `"tag_ids"`) {
		t.Fatalf("default product-list output should not include tag_ids: %s", stdout.String())
	}
}

func TestGetProductListPreservesTotalFromResponse(t *testing.T) {
	clearRQAMSCEnv(t)
	t.Setenv("RQAMS_TEST_PRODUCT_LIST_TOTAL", "37")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		total := os.Getenv("RQAMS_TEST_PRODUCT_LIST_TOTAL")
		_, _ = w.Write([]byte(`{"products":[{"id":"p1","name":"demo","start_date":"2026-01-01"}],"total":` + total + `}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "product-list", "--payload", "{}"}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected data: %#v", envelope["data"])
	}
	if data["total"] != float64(37) {
		t.Fatalf("expected total from test response to be preserved, got %#v in %s", data["total"], stdout.String())
	}
}

func TestGetProductListCustomFieldsAndLimit(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fields") != "id,name,product_state" {
			t.Fatalf("unexpected fields query: %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("limit") != "1" {
			t.Fatalf("unexpected limit query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"products":[{"id":"p1","name":"one","product_state":"normal"},{"id":"p2","name":"two","product_state":"normal"}],"total":2}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"get", "product-list", "--payload", `{"fields":["id","name","product_state"],"limit":1}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"returned": 1`) {
		t.Fatalf("expected returned count in limited output: %s", stdout.String())
	}
}

func TestGetProductListRawDoesNotApplyDefaultFields(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fields") != "" {
			t.Fatalf("raw request should not include fields: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"products":[],"permissions":[{"product_id":"p1"}],"total":0}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "product-list", "--payload", `{"raw":true}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"permissions"`) {
		t.Fatalf("raw output should keep permissions: %s", stdout.String())
	}
}

func TestGetProductListNDJSON(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fields") != "id,name,start_date,label" {
			t.Fatalf("unexpected fields query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"products":[{"id":"p1","name":"one","start_date":"2026-01-01","label":"live","user_id":1},{"id":"p2","name":"two","start_date":"2026-01-02","label":"paper","user_id":2}],"total":2}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "product-list", "--payload", `{"format":"ndjson"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two ndjson lines: %s", stdout.String())
	}
	if lines[0] != `{"id":"p1","label":"live","name":"one","start_date":"2026-01-01"}` {
		t.Fatalf("unexpected first line: %s", lines[0])
	}
	if strings.Contains(stdout.String(), `"ok"`) || strings.Contains(stdout.String(), `"user_id"`) {
		t.Fatalf("unexpected envelope or projected field: %s", stdout.String())
	}
}

func TestGetProductListRawNDJSON(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fields") != "" {
			t.Fatalf("raw ndjson request should not include fields: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"products":[{"id":"p1","name":"one","start_date":"2026-01-01","user_id":1}],"total":1}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "product-list", "--payload", `{"format":"ndjson","raw":true}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"user_id":1`) {
		t.Fatalf("raw ndjson should keep full product fields: %s", stdout.String())
	}
}

func TestGetProductListNDJSONFailureUsesEnvelope(t *testing.T) {
	clearRQAMSCEnv(t)
	configPath := writeConfig(t, "://bad-url", "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "product-list", "--payload", `{"format":"ndjson"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code == 0 {
		t.Fatalf("expected failure: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"ok": false`) {
		t.Fatalf("failure should use envelope: %s", stdout.String())
	}
}

func TestUnsupportedNDJSONUsesEnvelope(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"p1","name":"demo"}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "product", "--payload", `{"product_id":"p1","format":"ndjson"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code == 0 {
		t.Fatalf("expected unsupported ndjson failure: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"ok": false`) || !strings.Contains(stdout.String(), "does not support ndjson") {
		t.Fatalf("failure should use envelope with clear message: %s", stdout.String())
	}
}

func TestGetProductRequiresPayloadField(t *testing.T) {
	clearRQAMSCEnv(t)
	var stdout strings.Builder
	code := Run([]string{"get", "product", "--payload", "{}"}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code == 0 {
		t.Fatalf("expected failure: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"ok": false`) {
		t.Fatalf("expected failure envelope: %s", stdout.String())
	}
}

func TestInsertProductPostsProductObject(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("X-AMS-Workspace") != "w1" {
			t.Fatalf("missing workspace header")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if body["name"] != "demo" || body["start_date"] != "2026-01-01" {
			t.Fatalf("unexpected product body: %#v", body)
		}
		_, _ = w.Write([]byte(`{"id":"p1","name":"demo"}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"insert", "product", "--payload", `{"product":{"name":"demo","start_date":"2026-01-01"}}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"method": "POST"`) || !strings.Contains(stdout.String(), `"path": "products"`) {
		t.Fatalf("expected route metadata: %s", stdout.String())
	}
}

func TestInsertProductPostsTopLevelFields(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if body["name"] != "alias-demo" {
			t.Fatalf("unexpected product body: %#v", body)
		}
		if _, ok := body["format"]; ok {
			t.Fatalf("format should not be forwarded as a product field: %#v", body)
		}
		_, _ = w.Write([]byte(`{"id":"p2","name":"alias-demo"}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"insert", "product", "--payload", `{"name":"alias-demo","format":"json"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"command": "insert product"`) {
		t.Fatalf("expected insert product command envelope: %s", stdout.String())
	}
}

func TestCreateProductIsNotRegistered(t *testing.T) {
	clearRQAMSCEnv(t)
	var stdout strings.Builder
	code := Run([]string{"create", "product", "--payload", `{"name":"demo"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code == 0 {
		t.Fatalf("expected create product to be unsupported: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"unknown_command"`) {
		t.Fatalf("expected unknown command envelope: %s", stdout.String())
	}
}

func TestPermissionCommands(t *testing.T) {
	clearRQAMSCEnv(t)
	var sawGet bool
	var sawUpdate bool
	var sawDelete bool
	var sawBatch bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products":
			_, _ = w.Write([]byte(`{"products":[{"id":"p1","name":"prod"}]}`))
		case "/api/rqams/v2/product_groups":
			_, _ = w.Write([]byte(`{"product_groups":[{"id":"g1","name":"group"}]}`))
		case "/api/rqams/v2/products/p1/permissions":
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method for product permissions: %s", r.Method)
			}
			sawGet = true
			_, _ = w.Write([]byte(`[{"_id":"perm1","resource_id":"p1","resource_type":"product","user_id":123,"permission":"read_import_data","shared_by":1}]`))
		case "/api/rqams/v2/product_groups/g1/permissions":
			switch r.Method {
			case http.MethodPost:
				var body []map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("Decode returned error: %v", err)
				}
				if len(body) != 1 || body[0]["user_id"] != float64(123) || body[0]["permission"] != "write" || body[0]["permission_id"] != "perm1" {
					t.Fatalf("unexpected update body: %#v", body)
				}
				sawUpdate = true
				_, _ = w.Write([]byte(`{"effect_count":1,"error_messages":[]}`))
			case http.MethodDelete:
				var body []any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("Decode returned error: %v", err)
				}
				if len(body) != 1 || body[0] != "perm1" {
					t.Fatalf("unexpected delete body: %#v", body)
				}
				sawDelete = true
				_, _ = w.Write([]byte(`{"effect_count":1}`))
			default:
				t.Fatalf("unexpected method for group permissions: %s", r.Method)
			}
		case "/api/rqams/v2/products/permissions":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected batch method: %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			ids, ok := body["resource_ids"].([]any)
			if !ok || len(ids) != 2 || ids[0] != "p1" || ids[1] != "p2" {
				t.Fatalf("unexpected resource ids: %#v", body)
			}
			perms, ok := body["perm_info_list"].([]any)
			if !ok || len(perms) != 1 {
				t.Fatalf("unexpected perm_info_list: %#v", body)
			}
			sawBatch = true
			_, _ = w.Write([]byte(`{"effect_count":2,"error_messages":[]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	cases := [][]string{
		{"get", "permission-list", "--payload", `{"resource_type":"products","resource_id":"p1","fields":["id","user_id","permission"],"limit":1}`},
		{"update", "permission", "--payload", `{"resource_type":"product_groups","resource_id":"g1","permission":{"id":"perm1","user_id":123,"permission":"write"}}`},
		{"delete", "permission", "--payload", `{"resource_type":"product_groups","resource_id":"g1","permission_id":"perm1"}`},
		{"update", "permission-batch", "--payload", `{"resource_type":"products","resource_ids":["p1","p2"],"permissions":[{"user_id":123,"permission":"read_net_value","permission_id":"ignored"}]}`},
	}
	for _, args := range cases {
		var stdout strings.Builder
		code := Run(args, strings.NewReader(""), &stdout, &strings.Builder{})
		if code != 0 {
			t.Fatalf("Run(%v) returned code %d: %s", args, code, stdout.String())
		}
		if args[0] == "get" && (strings.Contains(stdout.String(), `"_id"`) || !strings.Contains(stdout.String(), `"id": "perm1"`)) {
			t.Fatalf("permission list should normalize id and apply fields: %s", stdout.String())
		}
	}
	if !sawGet || !sawUpdate || !sawDelete || !sawBatch {
		t.Fatalf("missing calls get=%v update=%v delete=%v batch=%v", sawGet, sawUpdate, sawDelete, sawBatch)
	}
}

func TestPermissionListNDJSON(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1/permissions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"id":"perm1","user_id":123,"permission":"read_net_value"},{"id":"perm2","user_id":456,"permission":"write"}]`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "permission-list", "--payload", `{"product_id":"p1","format":"ndjson"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"permission":"read_net_value"`) || strings.Contains(stdout.String(), `"ok"`) {
		t.Fatalf("unexpected ndjson output: %s", stdout.String())
	}
}

func TestGetProductGroupList(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/product_groups" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("fields") != "id,name,start_date,label" {
			t.Fatalf("unexpected fields query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"product_groups":[{"id":"g1","name":"group","start_date":"2026-01-01","label":"live","user_id":"u1"}]}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "product-group-list", "--payload", "{}"}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), `"user_id"`) {
		t.Fatalf("unexpected projected field in default output: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"product_groups"`) {
		t.Fatalf("expected product groups in output: %s", stdout.String())
	}
}

func TestGetProductGroupListNDJSON(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/product_groups" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"product_groups":[{"id":"g1","name":"one","start_date":"2026-01-01"},{"id":"g2","name":"two","start_date":"2026-01-02"}]}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "product-group-list", "--payload", `{"format":"ndjson"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two ndjson lines: %s", stdout.String())
	}
	if lines[0] != `{"id":"g1","name":"one","start_date":"2026-01-01"}` {
		t.Fatalf("unexpected first line: %s", lines[0])
	}
}

func TestGetProductGroupResolvesName(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/product_groups":
			_, _ = w.Write([]byte(`{"product_groups":[{"id":"g1","name":"group"}]}`))
		case "/api/rqams/v2/product_groups/g1":
			_, _ = w.Write([]byte(`{"id":"g1","name":"group"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "product-group", "--payload", `{"group_id_or_name":"group"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"resolved_path": "product_groups/g1"`) {
		t.Fatalf("expected resolved path: %s", stdout.String())
	}
}

func TestUpdateProductGroupTransformsProducts(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/product_groups/g1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPatch {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("invalid JSON body: %v", err)
		}
		if _, ok := body["id"]; ok {
			t.Fatalf("id should not be forwarded: %#v", body)
		}
		ids, ok := body["product_ids"].([]any)
		if !ok || len(ids) != 2 || ids[0] != "p1" || ids[1] != "p2" {
			t.Fatalf("unexpected product_ids: %#v", body)
		}
		_, _ = w.Write([]byte(`{"updated":true}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"update", "product-group", "--payload", `{"group_id":"g1","update_fields":{"id":"ignored","products":[{"id":"p1"},{"id":"p2"}]}}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
}

func TestDeleteProductGroup(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/product_groups/g1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodDelete {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"deleted":true}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"delete", "product-group", "--payload", `{"group_id":"g1"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
}

func TestGetBalanceSeriesSupportsProductGroup(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/product_groups":
			_, _ = w.Write([]byte(`{"product_groups":[{"id":"g1","name":"group"}]}`))
		case "/api/rqams/v2/product_groups/g1/balance_series":
			if r.URL.Query().Get("position_fields") != "total_equity,unit_net_value" {
				t.Fatalf("unexpected query: %s", r.URL.RawQuery)
			}
			if r.URL.Query().Get("flat_position") != "true" {
				t.Fatalf("flat_position should be true: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"date":"2026-01-01","total_equity":1}]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"get", "balance-series", "--payload", `{"product_group_id_or_name":"group","start_date":"2026-01-01","fields":["total_equity","unit_net_value"],"format":"ndjson"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if strings.TrimSpace(stdout.String()) != `{"date":"2026-01-01","total_equity":1}` {
		t.Fatalf("unexpected ndjson output: %s", stdout.String())
	}
}

func TestInsertPositionStatementPollsTask(t *testing.T) {
	clearRQAMSCEnv(t)
	uploadPath := filepath.Join(t.TempDir(), "position.xlsx")
	if err := os.WriteFile(uploadPath, []byte("position"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1/asset_units/a1:upload_positions_statement" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		switch r.Method {
		case http.MethodPost:
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("ParseMultipartForm returned error: %v", err)
			}
			if r.FormValue("broker") != "ricequant" {
				t.Fatalf("unexpected broker: %s", r.FormValue("broker"))
			}
			if r.MultipartForm.File["file"] == nil {
				t.Fatalf("missing file upload")
			}
			_, _ = w.Write([]byte(`{"task_id":"task-1"}`))
		case http.MethodGet:
			if r.URL.Query().Get("task_id") != "task-1" {
				t.Fatalf("missing task_id query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"status":"SUCCESS","data":{"inserted":1}}`))
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"insert", "position-statement", "--payload", `{"product_id":"p1","asset_unit_id":"a1","file_path":` + quote(uploadPath) + `}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"inserted": 1`) {
		t.Fatalf("expected poll result: %s", stdout.String())
	}
}

func TestReconciliationCommands(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products":
			if r.URL.Query().Get("fields") != "id,name" {
				t.Fatalf("unexpected product lookup query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"products":[{"id":"p1","name":"prod"}]}`))
		case "/api/rqams/v2/products:batch_get_reconciliation_list":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			ids, ok := body["product_ids"].([]any)
			if !ok || len(ids) != 1 || ids[0] != "p1" {
				t.Fatalf("unexpected product ids: %#v", body)
			}
			if body["start_date"] != "2026-01-01" || body["end_date"] != "2026-01-31" {
				t.Fatalf("unexpected date range: %#v", body)
			}
			_, _ = w.Write([]byte(`[{"product_id":"p1","reconciliation_list":[{"date":"2026-01-31"}]}]`))
		case "/api/rqams/v2/products/p1:get_reconciliation_diff":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			fields, ok := body["fields"].([]any)
			if !ok || len(fields) != 2 || fields[0] != "positions" || fields[1] != "net_asset" {
				t.Fatalf("unexpected fields: %#v", body)
			}
			if body["date"] != "2026-01-31" {
				t.Fatalf("unexpected date: %#v", body)
			}
			_, _ = w.Write([]byte(`{"positions":[],"net_asset":[]}`))
		case "/api/rqams/v2/products/p1:reconciliation":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			if body["date"] != "2026-01-31" || body["done"] != true || body["description"] != "checked" {
				t.Fatalf("unexpected update body: %#v", body)
			}
			_, _ = w.Write([]byte(`{"effect_count":1}`))
		case "/api/rqams/v2/products/p1:auto_reconciliation":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			if body["date"] != "2026-01-31" {
				t.Fatalf("unexpected auto body: %#v", body)
			}
			_, _ = w.Write([]byte(`""`))
		case "/api/rqams/v2/products/p1:undo_auto_reconciliation":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			if body["date"] != "2026-01-31" {
				t.Fatalf("unexpected undo body: %#v", body)
			}
			_, _ = w.Write([]byte(`{"effect_count":1}`))
		case "/api/rqams/v2/products/p1/asset_units/a1:get_reconciliation_diff":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			if body["date"] != "2026-01-31" {
				t.Fatalf("unexpected asset-unit diff body: %#v", body)
			}
			_, _ = w.Write([]byte(`[{"order_book_id":"000001.XSHE"}]`))
		case "/api/rqams/v2/products/p1/asset_units/a1:reconciliation_positions_statement":
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			if r.URL.Query().Get("date") != "2026-01-31" {
				t.Fatalf("unexpected query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"is_consistence":true}`))
		case "/api/rqams/v2/products/asset_units/positions_statement:get_latest":
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			_, _ = w.Write([]byte(`[{"product_id":"p1","asset_unit_list":[]}]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	cases := [][]string{
		{"get", "reconciliation-list", "--payload", `{"product_ids_or_names":["prod"],"start_date":"2026-01-01","end_date":"2026-01-31"}`},
		{"get", "reconciliation-diff", "--payload", `{"product_id":"p1","date":"2026-01-31","fields":["positions","net_asset"]}`},
		{"update", "reconciliation", "--payload", `{"product_id":"p1","date":"2026-01-31","done":true,"description":"checked"}`},
		{"update", "reconciliation", "--payload", `{"product_id":"p1","date":"2026-01-31","action":"auto"}`},
		{"update", "reconciliation", "--payload", `{"product_id":"p1","date":"2026-01-31","action":"undo_auto"}`},
		{"get", "reconciliation-asset-unit-diff", "--payload", `{"product_id":"p1","asset_unit_id":"a1","date":"2026-01-31"}`},
		{"get", "reconciliation-position-statement", "--payload", `{"product_id":"p1","asset_unit_id":"a1","date":"2026-01-31"}`},
		{"get", "position-statement-latest-list", "--payload", `{}`},
	}
	for _, args := range cases {
		var stdout strings.Builder
		code := Run(args, strings.NewReader(""), &stdout, &strings.Builder{})
		if code != 0 {
			t.Fatalf("Run(%v) returned code %d: %s", args, code, stdout.String())
		}
		if !strings.Contains(stdout.String(), `"ok": true`) {
			t.Fatalf("Run(%v) should succeed: %s", args, stdout.String())
		}
	}
}

func TestUpdateReconciliationRejectsUnknownAction(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called for invalid action")
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"update", "reconciliation", "--payload", `{"product_id":"p1","date":"2026-01-31","action":"bad"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code == 0 {
		t.Fatalf("expected invalid action failure: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "must be one of mark, auto, undo_auto") {
		t.Fatalf("unexpected error: %s", stdout.String())
	}
}

func TestValuationReportListDeleteAndDownload(t *testing.T) {
	clearRQAMSCEnv(t)
	saveDir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products/p1/valuation_reports":
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method for list: %s", r.Method)
			}
			_, _ = w.Write([]byte(`{"valuation_reports":[{"valuation_report_id":"vr1","file_name":"report.xlsx","date":"2026-01-01"}]}`))
		case "/api/rqams/v2/products/p1/valuation_reports/vr1/src_file":
			w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
			_, _ = w.Write([]byte("xlsx"))
		case "/api/rqams/v2/products/p1/valuation_reports:batch_delete":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method for delete: %s", r.Method)
			}
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), `"dates":["2026-01-01"]`) {
				t.Fatalf("unexpected delete body: %s", string(raw))
			}
			_, _ = w.Write([]byte(`{"deleted":1}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "valuation-report-list", "--payload", `{"product_id":"p1","fields":["valuation_report_id"],"limit":1}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 || !strings.Contains(stdout.String(), `"valuation_report_id": "vr1"`) {
		t.Fatalf("unexpected list result code=%d output=%s", code, stdout.String())
	}

	stdout.Reset()
	code = Run([]string{"get", "valuation-report-file", "--payload", `{"product_id":"p1","valuation_report_id":"vr1","save_path":` + quote(saveDir) + `,"file_name":"report.xlsx"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("download returned code %d: %s", code, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(saveDir, "report.xlsx")); err != nil {
		t.Fatalf("expected downloaded file: %v", err)
	}

	stdout.Reset()
	code = Run([]string{"delete", "valuation-report", "--payload", `{"product_id":"p1","dates":["2026-01-01"]}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("delete returned code %d: %s", code, stdout.String())
	}
}

func TestValuationReportListNDJSON(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1/valuation_reports" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"valuation_reports":[{"valuation_report_id":"vr1","date":"2026-01-01"},{"valuation_report_id":"vr2","date":"2026-01-02"}]}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"get", "valuation-report-list", "--payload", `{"product_id":"p1","format":"ndjson"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 || lines[0] != `{"date":"2026-01-01","valuation_report_id":"vr1"}` {
		t.Fatalf("unexpected ndjson output: %s", stdout.String())
	}
}

func TestValuationReportListNormalizesArrayResponse(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1/valuation_reports" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"valuation_report_id":"vr1","file_name":"report.xlsx","date":"2026-01-01","source":"open_api"}]`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"get", "valuation-report-list", "--payload", `{"product_id":"p1","fields":["valuation_report_id","file_name"],"limit":1}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"valuation_reports": [`) || !strings.Contains(stdout.String(), `"total": 1`) {
		t.Fatalf("array response was not normalized: %s", stdout.String())
	}
}

func TestInsertValuationReportUploadsXlsxOnly(t *testing.T) {
	clearRQAMSCEnv(t)
	dir := t.TempDir()
	xlsxPath := filepath.Join(dir, "report.xlsx")
	txtPath := filepath.Join(dir, "ignore.txt")
	if err := os.WriteFile(xlsxPath, []byte("xlsx"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := os.WriteFile(txtPath, []byte("txt"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	uploads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1/valuation_reports" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm returned error: %v", err)
		}
		uploads++
		if r.MultipartForm.File["file"][0].Filename != "report.xlsx" {
			t.Fatalf("unexpected file: %#v", r.MultipartForm.File["file"])
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"insert", "valuation-report", "--payload", `{"product_id":"p1","file_paths":[` + quote(dir) + `]}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if uploads != 1 {
		t.Fatalf("expected one xlsx upload, got %d", uploads)
	}
}

func TestInsertValuationReportJSONKeepsReplaceDatesAsList(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1/valuation_reports" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		dates, ok := body["replace_dates"].([]any)
		if !ok || len(dates) != 1 || dates[0] != "2026-01-08" {
			t.Fatalf("replace_dates should stay a JSON array: %#v", body["replace_dates"])
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"insert", "valuation-report", "--payload", `{"product_id":"p1","replace_dates":["2026-01-08"],"valuation_reports":[{"date":"2026-01-08","total_equity":1000000,"units":1000000,"unit_net_value":1,"positions":[]}]}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
}

func TestCustodianAndUnitEventCommands(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products/p1/custodian_events:batch_insert":
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), `"product_id":"p1"`) {
				t.Fatalf("insert body missing product_id: %s", string(raw))
			}
			_, _ = w.Write([]byte(`{"inserted":1}`))
		case "/api/rqams/v2/products/p1/unit_events/e1":
			if r.Method != http.MethodPut {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			raw, _ := io.ReadAll(r.Body)
			if strings.Contains(string(raw), `"id"`) {
				t.Fatalf("unit update body should remove id: %s", string(raw))
			}
			_, _ = w.Write([]byte(`{"updated":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"insert", "custodian-event", "--payload", `{"product_id":"p1","custodian_events":[{"custodian_event_type":"subscription"}]}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("insert returned code %d: %s", code, stdout.String())
	}

	stdout.Reset()
	code = Run([]string{"update", "unit-event", "--payload", `{"product_id":"p1","unit_event":{"id":"e1","date":"2026-01-01"}}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("update returned code %d: %s", code, stdout.String())
	}
}

func TestCustomizedInstrumentAndBenchmarkCommands(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/customized_instruments":
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			_, _ = w.Write([]byte(`{"customized_instruments":[{"id":"ci1","name":"ins","extra":1}]}`))
		case "/api/rqams/v2/customized_instruments:batch_delete":
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), `"ci1"`) {
				t.Fatalf("unexpected delete body: %s", string(raw))
			}
			_, _ = w.Write([]byte(`{"deleted":1}`))
		case "/api/rqams/v2/customized_benchmarks":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected benchmark method: %s", r.Method)
			}
			_, _ = w.Write([]byte(`{"inserted_id":"cb1"}`))
		case "/api/rqams/v2/customized_benchmarks/cb1":
			_, _ = w.Write([]byte(`{"id":"cb1","name":"bench"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "customized-instrument-list", "--payload", `{"fields":["id","name"],"format":"ndjson"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 || strings.TrimSpace(stdout.String()) != `{"id":"ci1","name":"ins"}` {
		t.Fatalf("unexpected instrument list code=%d output=%s", code, stdout.String())
	}

	stdout.Reset()
	code = Run([]string{"delete", "customized-instrument", "--payload", `{"customized_ins_id":"ci1"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("delete instrument returned code %d: %s", code, stdout.String())
	}

	stdout.Reset()
	code = Run([]string{"insert", "customized-benchmark", "--payload", `{"customized_benchmark":{"name":"bench"}}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 || !strings.Contains(stdout.String(), `"id": "cb1"`) {
		t.Fatalf("unexpected benchmark insert code=%d output=%s", code, stdout.String())
	}
}

func TestWeeklyReport(t *testing.T) {
	clearRQAMSCEnv(t)
	saveDir := t.TempDir()
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products/p1/weekly_net_value_report":
			calls++
			if calls == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"task_id":"weekly-task"}`))
				return
			}
			if r.URL.Query().Get("task_id") != "weekly-task" {
				t.Fatalf("missing task_id query: %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
			_, _ = w.Write([]byte("weekly"))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "weekly-net-value-report", "--payload", `{"product_id":"p1","save_path":` + quote(saveDir) + `,"file_name":"weekly.xlsx"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("weekly report returned code %d: %s", code, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(saveDir, "weekly.xlsx")); err != nil {
		t.Fatalf("expected weekly report file: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected task request and download poll, got %d calls", calls)
	}
}

func TestFileUploadSessionFileCommandIsNotPublic(t *testing.T) {
	clearRQAMSCEnv(t)
	configPath := writeConfig(t, "http://127.0.0.1", "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"insert", "file-upload-session-file", "--payload", `{}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code == 0 {
		t.Fatalf("expected unknown command: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"unknown_command"`) {
		t.Fatalf("expected unknown command envelope: %s", stdout.String())
	}
}

func TestGetTradeList(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1/trades" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("start_date") != "2026-01-01" || r.URL.Query().Get("end_date") != "2026-01-31" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"trades":[{"id":"t1"},{"id":"t2"}]}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"get", "trade-list", "--payload", `{"product_id":"p1","start_date":"2026-01-01","end_date":"2026-01-31","limit":1}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"returned": 1`) {
		t.Fatalf("expected limited trade output: %s", stdout.String())
	}
}

func TestGetTradeListNDJSON(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1/trades" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"trades":[{"id":"t1","date":"2026-01-01"},{"id":"t2","date":"2026-01-02"}],"total":2}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"get", "trade-list", "--payload", `{"product_id":"p1","format":"ndjson"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 || lines[0] != `{"date":"2026-01-01","id":"t1"}` {
		t.Fatalf("unexpected ndjson output: %s", stdout.String())
	}
}

func TestInsertTradeBatchesAndSetsSource(t *testing.T) {
	clearRQAMSCEnv(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1/trades:batch_insert" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		var body []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		calls++
		if len(body) != 2 {
			t.Fatalf("expected chunk_size below 500 to be bounded upward: %#v", body)
		}
		for _, trade := range body {
			if trade["source"] != "open_api" {
				t.Fatalf("source should be forced to open_api: %#v", trade)
			}
		}
		_, _ = w.Write([]byte(`[{"inserted":1}]`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"insert", "trade", "--payload", `{"product_id":"p1","chunk_size":1,"trades":[{"id":"t1","source":"manual"},{"id":"t2"}]}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if calls != 1 {
		t.Fatalf("expected one bounded batch call, got %d", calls)
	}
	if !strings.Contains(stdout.String(), `"chunks": 1`) || !strings.Contains(stdout.String(), `"chunk_size": 500`) {
		t.Fatalf("expected chunk metadata: %s", stdout.String())
	}
}

func TestInsertSettlementTradeUploadsAndPolls(t *testing.T) {
	clearRQAMSCEnv(t)
	uploadPath := filepath.Join(t.TempDir(), "settlement.csv")
	if err := os.WriteFile(uploadPath, []byte("date,account\n2026-01-01,stock\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	var uploadSeen bool
	var pollSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1:upload_settlement_trade_file" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		switch r.Method {
		case "POST":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("ParseMultipartForm returned error: %v", err)
			}
			if r.FormValue("account") != "stock" || r.FormValue("asset_unit_id") != "au1" {
				t.Fatalf("unexpected form fields: %#v", r.Form)
			}
			if _, _, err := r.FormFile("file"); err != nil {
				t.Fatalf("expected file field: %v", err)
			}
			uploadSeen = true
			_, _ = w.Write([]byte(`{"task_id":"task1"}`))
		case "GET":
			if r.URL.Query().Get("task_id") != "task1" {
				t.Fatalf("unexpected query: %s", r.URL.RawQuery)
			}
			pollSeen = true
			_, _ = w.Write([]byte(`{"status":"SUCCESS","data":{"imported":2}}`))
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"insert", "settlement-trade", "--payload", `{"product_id":"p1","account_name":"stock","asset_unit_id":"au1","file_paths":["` + filepath.ToSlash(uploadPath) + `"]}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !uploadSeen || !pollSeen {
		t.Fatalf("expected upload and poll, upload=%v poll=%v", uploadSeen, pollSeen)
	}
	if !strings.Contains(stdout.String(), `"imported": 2`) {
		t.Fatalf("expected final poll data: %s", stdout.String())
	}
}

func TestDeleteTradeUsesFilters(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1/trades:batch_delete_by_date" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("start_date") != "2026-01-01" ||
			r.URL.Query().Get("end_date") != "2026-01-31" ||
			r.URL.Query().Get("sources") != "open_api,manual" ||
			r.URL.Query().Get("account_names") != "stock" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"status":"SUCCESS","data":{"deleted":3}}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"delete", "trade", "--payload", `{"product_id":"p1","start_date":"2026-01-01","end_date":"2026-01-31","sources":["open_api","manual"],"account_names":["stock"]}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"deleted": 3`) {
		t.Fatalf("expected delete data: %s", stdout.String())
	}
}

func TestDeleteTradeByIDs(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1/trades:batch_delete" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		var body []string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if len(body) != 2 || body[0] != "t1" || body[1] != "t2" {
			t.Fatalf("unexpected delete ids: %#v", body)
		}
		_, _ = w.Write([]byte(`{"deleted":2}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"delete", "trade", "--payload", `{"product_id":"p1","trade_ids":["t1","t2"]}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"delete_mode": "trade_ids"`) {
		t.Fatalf("expected delete mode metadata: %s", stdout.String())
	}
}

func TestGetBalance(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1/balance" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("date") != "2026-01-05" || r.URL.Query().Get("flat_position") != "true" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("fields") != "total_equity,unit_net_value" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"total_equity":100,"unit_net_value":1.01,"positions":[{"order_book_id":"CNY"}]}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "balance", "--payload", `{"product_id":"p1","date":"2026-01-05","fields":["total_equity","unit_net_value"]}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), `"positions"`) {
		t.Fatalf("expected projected balance fields only: %s", stdout.String())
	}
}

func TestGetBalanceSummaryIsNotExposed(t *testing.T) {
	clearRQAMSCEnv(t)
	configPath := writeConfig(t, "https://example.test", "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "balance-summary", "--payload", `{"product_id":"p1"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code == 0 {
		t.Fatalf("get balance-summary should not be exposed: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"unknown_command"`) {
		t.Fatalf("get balance-summary should return unknown_command: %s", stdout.String())
	}
}

func TestIndicatorCommandsUseProductAndGroupPaths(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products":
			_, _ = w.Write([]byte(`{"products":[{"id":"p1","name":"prod"}]}`))
		case "/api/rqams/v2/product_groups":
			_, _ = w.Write([]byte(`{"product_groups":[{"id":"g1","name":"group"}]}`))
		case "/api/rqams/v2/products/p1/indicators":
			if r.URL.Query().Get("start_date") != "2026-01-01" {
				t.Fatalf("unexpected indicator query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"annualized_returns":0.1}`))
		case "/api/rqams/v2/product_groups/g1/indicators_series":
			if r.URL.Query().Get("indicators") != "returns,volatility" {
				t.Fatalf("unexpected series query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"series":[]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	cases := [][]string{
		{"get", "indicator", "--payload", `{"product_like_id_or_name":"prod","start_date":"2026-01-01"}`},
		{"get", "indicator-series", "--payload", `{"product_group_id_or_name":"group","indicators":["returns","volatility"]}`},
	}
	for _, args := range cases {
		var stdout strings.Builder
		code := Run(args, strings.NewReader(""), &stdout, &strings.Builder{})
		if code != 0 {
			t.Fatalf("Run(%v) returned code %d: %s", args, code, stdout.String())
		}
	}
}

func TestCustomizedIndicatorCommands(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/p1/customized_indicators" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		switch r.Method {
		case "GET", "DELETE":
		case "POST", "PATCH":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			if body["custom_metric"] == nil {
				t.Fatalf("unexpected body: %#v", body)
			}
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	cases := [][]string{
		{"get", "customized-indicator", "--payload", `{"product_id":"p1"}`},
		{"insert", "customized-indicator", "--payload", `{"product_id":"p1","customized_indicators":{"custom_metric":1}}`},
		{"update", "customized-indicator", "--payload", `{"product_id":"p1","customized_indicators":{"custom_metric":2}}`},
		{"delete", "customized-indicator", "--payload", `{"product_id":"p1"}`},
	}
	for _, args := range cases {
		var stdout strings.Builder
		code := Run(args, strings.NewReader(""), &stdout, &strings.Builder{})
		if code != 0 {
			t.Fatalf("Run(%v) returned code %d: %s", args, code, stdout.String())
		}
	}
}

func TestInvestmentOverviewCommands(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products":
			_, _ = w.Write([]byte(`{"products":[{"id":"p1","name":"prod"}]}`))
		case "/api/rqams/v2/product_group_overview/indicators_v2":
			if r.Method != "GET" || r.URL.Query().Get("product_or_group_ids") != "p1" {
				t.Fatalf("unexpected summary request: %s %s", r.Method, r.URL.String())
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"p1"}]}`))
		case "/api/rqams/v2/product_group_overview/returns_v2":
			if r.Method == "GET" {
				if r.URL.Query().Get("task_id") != "task1" {
					t.Fatalf("unexpected returns poll query: %s", r.URL.RawQuery)
				}
				_, _ = w.Write([]byte(`{"status":"SUCCESS","data":[{"return":0.1}]}`))
				return
			}
			if r.Method != "POST" {
				t.Fatalf("unexpected returns method: %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			if body["benchmarks"] != "000300.XSHG" || body["product_or_group_ids"] != "p1" {
				t.Fatalf("unexpected returns body: %#v", body)
			}
			_, _ = w.Write([]byte(`{"task_id":"task1"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	cases := [][]string{
		{"get", "investment-overview-summary-indicator", "--payload", `{"product_like_ids_or_names":["prod"],"start_date":"2026-01-01","end_date":"2026-01-31"}`},
		{"get", "investment-overview-returns-series", "--payload", `{"product_like_ids_or_names":["prod"],"start_date":"2026-01-01","end_date":"2026-01-31","benchmark_id":"000300.XSHG"}`},
	}
	for _, args := range cases {
		var stdout strings.Builder
		code := Run(args, strings.NewReader(""), &stdout, &strings.Builder{})
		if code != 0 {
			t.Fatalf("Run(%v) returned code %d: %s", args, code, stdout.String())
		}
	}
}

func TestPerformanceAttributionPollsResult(t *testing.T) {
	clearRQAMSCEnv(t)
	var polls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products/p1/performance_attributions":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			if body["benchmark"] != "index,000300.XSHG" || body["template"] != "equity/brinson" || body["only_returns_decomposition"] != true {
				t.Fatalf("unexpected attribution body: %#v", body)
			}
			_, _ = w.Write([]byte(`{"id":"task1"}`))
		case "/api/rqams/v2/products/p1/performance_attributions/task1":
			polls++
			if polls == 1 {
				_, _ = w.Write([]byte(`{"status":"DOING"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"SUCCESS","result":[{"effect":1}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"get", "returns-decomposition", "--payload", `{"product_id":"p1","start_date":"2026-01-01","end_date":"2026-01-31","benchmark_id":"000300.XSHG"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if polls != 2 || !strings.Contains(stdout.String(), `"effect": 1`) {
		t.Fatalf("unexpected attribution result, polls=%d output=%s", polls, stdout.String())
	}
}

func TestTradingAnalysisCommands(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products/p1/trading_analysis_list":
			if r.URL.Query().Get("task_id") == "task1" {
				_, _ = w.Write([]byte(`{"status":"SUCCESS","data":[{"order_book_id":"000001.XSHE","period_pnl":10}]}`))
				return
			}
			if r.URL.Query().Get("start_date") != "2026-01-01" {
				t.Fatalf("unexpected list query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"task_id":"task1"}`))
		case "/api/rqams/v2/products/p1/single_trading_analysis":
			if r.URL.Query().Get("order_book_id") != "000001.XSHE" ||
				r.URL.Query().Get("asset_class") != "stock" ||
				r.URL.Query().Get("direction") != "long" {
				t.Fatalf("unexpected detail query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":{"win_rate":0.6}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	cases := [][]string{
		{"get", "trading-analysis-list", "--payload", `{"product_id":"p1","start_date":"2026-01-01"}`},
		{"get", "trading-analysis", "--payload", `{"product_id":"p1","order_book_id":"000001.XSHE","asset_class":"stock","direction":"long"}`},
	}
	for _, args := range cases {
		var stdout strings.Builder
		code := Run(args, strings.NewReader(""), &stdout, &strings.Builder{})
		if code != 0 {
			t.Fatalf("Run(%v) returned code %d: %s", args, code, stdout.String())
		}
	}
}

func TestLegacyPaperTradingCommandsAreNotExposed(t *testing.T) {
	clearRQAMSCEnv(t)
	configPath := writeConfig(t, "https://example.invalid", "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	cases := [][]string{
		{"get", "paper-trading-channel-list", "--payload", "{}"},
		{"get", "paper-trading-v2-list", "--payload", `{"limit":1}`},
		{"get", "paper-trading-v2-signal-list", "--payload", `{"product_id":"p1","fields":["id","status"]}`},
		{"get", "paper-trading-v2-signal", "--payload", `{"product_id":"p1","signal_id":"v2s1"}`},
	}
	for _, args := range cases {
		var stdout strings.Builder
		code := Run(args, strings.NewReader(""), &stdout, &strings.Builder{})
		if code == 0 {
			t.Fatalf("Run(%v) unexpectedly succeeded: %s", args, stdout.String())
		}
		if !strings.Contains(stdout.String(), `"code": "unknown_command"`) {
			t.Fatalf("Run(%v) should return unknown_command: %s", args, stdout.String())
		}
	}
}

func TestUnifiedPaperTradingCommands(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products:list_paper_trading_channels":
			_, _ = w.Write([]byte(`[{"_id":"c1","product_id":"p1","stock_min_fee":5,"futures_float_amount":10}]`))
		case "/api/rqams/v2/products:list_paper_trading_v2":
			_, _ = w.Write([]byte(`[{"product_id":"p2","init_amount":1000,"algo":"open"}]`))
		case "/api/rqams/v2/products/p1/paper_trading_signals":
			_, _ = w.Write([]byte(`{"signals":[{"id":"s1"},{"id":"s2"}]}`))
		case "/api/rqams/v2/products/p1/paper_trading_signals/s1:get_details":
			_, _ = w.Write([]byte(`{"id":"s1","version":"v1"}`))
		case "/api/rqams/v2/products/p2/paper_trading_v2/signals":
			if r.URL.Query().Get("fields") != "id,status" {
				t.Fatalf("unexpected query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"signals":[{"id":"v2s1","status":"done"}]}`))
		case "/api/rqams/v2/products/p2/paper_trading_v2/signals/v2s1":
			_, _ = w.Write([]byte(`{"id":"v2s1","version":"v2"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	cases := [][]string{
		{"get", "paper-trading-list", "--payload", `{"fields":["product_id","strategy_model"],"limit":2}`},
		{"get", "paper-trading", "--payload", `{"product_id":"p1"}`},
		{"get", "paper-trading-signal-list", "--payload", `{"product_id":"p1","limit":1}`},
		{"get", "paper-trading-signal", "--payload", `{"product_id":"p1","signal_id":"s1"}`},
		{"get", "paper-trading-signal-list", "--payload", `{"product_id":"p2","fields":["id","status"]}`},
		{"get", "paper-trading-signal", "--payload", `{"product_id":"p2","signal_id":"v2s1"}`},
	}
	for _, args := range cases {
		var stdout strings.Builder
		code := Run(args, strings.NewReader(""), &stdout, &strings.Builder{})
		if code != 0 {
			t.Fatalf("Run(%v) returned code %d: %s", args, code, stdout.String())
		}
		if strings.Contains(stdout.String(), `"version"`) || strings.Contains(stdout.String(), `"_id"`) || strings.Contains(stdout.String(), `"signal_id"`) {
			t.Fatalf("Run(%v) exposed internal paper trading fields: %s", args, stdout.String())
		}
	}
}

func TestUnifiedPaperTradingMutations(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products:list_paper_trading_channels":
			_, _ = w.Write([]byte(`[{"_id":"c1","product_id":"p1","stock_min_fee":5,"futures_float_amount":10,"slippage_ticks":2}]`))
		case "/api/rqams/v2/products:list_paper_trading_v2":
			_, _ = w.Write([]byte(`[{"product_id":"p2","init_amount":1000,"algo":"open"}]`))
		case "/api/rqams/v2/products/paper_trading_channels:batch_upsert":
			if r.Method != "POST" {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			if body["futures_float_amount"] != nil || body["slippage_ticks"] != nil {
				t.Fatalf("mutually exclusive fields were not cleared: %#v", body)
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/api/rqams/v2/products/p2/paper_trading_v2":
			if r.Method != "PATCH" && r.Method != "DELETE" {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
		case "/api/rqams/v2/products/p1/paper_trading_channels/c1:async_delete":
			_, _ = w.Write([]byte(`{"data":{"deleted":1}}`))
		case "/api/rqams/v2/products/p1/paper_trading_channels/c1:async_recompute":
			_, _ = w.Write([]byte(`{"data":{"task_id":"r1"}}`))
		case "/api/rqams/v2/products/p2/paper_trading_v2:recompute":
			_, _ = w.Write([]byte(`{"data":{"task_id":"r2"}}`))
		case "/api/rqams/v2/products/p1/paper_trading_channels/paper_trading_signals":
			_, _ = w.Write([]byte(`{"data":{"effect_count":1}}`))
		case "/api/rqams/v2/products/p2/paper_trading_v2/signals:async_delete":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			ids, ok := body["signal_ids"].([]any)
			if !ok || len(ids) != 1 || ids[0] != "s2" {
				t.Fatalf("unexpected signal_ids body: %#v", body)
			}
			_, _ = w.Write([]byte(`{"data":{"effect_count":1}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	cases := [][]string{
		{"update", "paper-trading", "--payload", `{"product_id":"p1","update_fields":{"futures_float_rate":0.2,"slippage_rate":0.01}}`},
		{"update", "paper-trading", "--payload", `{"product_id":"p2","update_fields":{"min_fee":1}}`},
		{"delete", "paper-trading", "--payload", `{"product_id":"p1"}`},
		{"delete", "paper-trading", "--payload", `{"product_id":"p2"}`},
		{"recompute", "paper-trading", "--payload", `{"product_id":"p1","date":"2026-01-05"}`},
		{"recompute", "paper-trading", "--payload", `{"product_id":"p2","date":"2026-01-05"}`},
		{"delete", "paper-trading-signal", "--payload", `{"product_id":"p1","start_date":"2026-01-01","end_date":"2026-01-31"}`},
		{"delete", "paper-trading-signal", "--payload", `{"product_id":"p2","signal_ids":["s2"]}`},
	}
	for _, args := range cases {
		var stdout strings.Builder
		code := Run(args, strings.NewReader(""), &stdout, &strings.Builder{})
		if code != 0 {
			t.Fatalf("Run(%v) returned code %d: %s", args, code, stdout.String())
		}
	}
}

func TestInsertPaperTradingV2(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/paper_trading_v2" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm returned error: %v", err)
		}
		if r.Form.Get("name") != "demo" ||
			r.Form.Get("benchmark") != "index,000300.XSHG" ||
			r.Form.Get("start_date") != "2026-01-01" ||
			r.Form.Get("init_amount") != "1000000" ||
			r.Form.Get("algo") != "open" ||
			r.Form.Get("strategy_model") != "equity_long" ||
			r.Form.Get("tag_ids") != "t1,t2" {
			t.Fatalf("unexpected form: %#v", r.Form)
		}
		_, _ = w.Write([]byte(`{"data":{"product_id":"p1","paper_trading_id":"pt1"}}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"insert", "paper-trading", "--payload", `{"name":"demo","benchmark":"index,000300.XSHG","start_date":"2026-01-01","init_amount":1000000,"algo":"open","tag_ids":["t1","t2"]}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), `"resolved_version"`) || strings.Contains(stdout.String(), `"resolved_template"`) {
		t.Fatalf("insert output should not expose internal route metadata: %s", stdout.String())
	}
}

func TestInsertPaperTradingV2Multipart(t *testing.T) {
	clearRQAMSCEnv(t)
	uploadPath := filepath.Join(t.TempDir(), "signals.csv")
	if err := os.WriteFile(uploadPath, []byte("order_book_id,target_weight\n000001.XSHE,1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/paper_trading_v2" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Fatalf("expected multipart content type: %s", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm returned error: %v", err)
		}
		if r.Form.Get("name") != "demo" || r.Form.Get("strategy_model") != "equity_long" {
			t.Fatalf("unexpected form: %#v", r.Form)
		}
		if len(r.MultipartForm.File["files"]) != 1 {
			t.Fatalf("expected one uploaded file: %#v", r.MultipartForm.File)
		}
		_, _ = w.Write([]byte(`{"data":{"product_id":"p1","uploaded":1}}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"insert", "paper-trading", "--payload", `{"name":"demo","benchmark":"index,000300.XSHG","start_date":"2026-01-01","init_amount":1000000,"algo":"open","file_path":"` + filepath.ToSlash(uploadPath) + `"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
}

func TestInsertPaperTradingConventional(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products/paper_trading_v2:create_conventional" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm returned error: %v", err)
		}
		if r.Form.Get("name") != "demo" ||
			r.Form.Get("benchmark") != "index,000300.XSHG" ||
			r.Form.Get("start_date") != "2026-01-01" ||
			r.Form.Get("init_amount") != "1000000" ||
			r.Form.Get("stock_min_fee") != "5" ||
			r.Form.Get("stock_commission_rate") != "0.0003" ||
			r.Form.Get("loan_rate") != "0.06" ||
			r.Form.Get("margin_rate") != "0.5" ||
			r.Form.Get("strategy_category") != "index_enhanced" {
			t.Fatalf("unexpected form: %#v", r.Form)
		}
		if r.Form.Get("algo") != "" || r.Form.Get("strategy_model") != "" {
			t.Fatalf("conventional request should not include v2 algo fields: %#v", r.Form)
		}
		_, _ = w.Write([]byte(`{"data":{"product_id":"p1","channel_id":"c1"}}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"insert", "paper-trading", "--payload", `{"template":"conventional","name":"demo","benchmark":"index,000300.XSHG","start_date":"2026-01-01","init_amount":1000000,"stock_min_fee":5,"stock_commission_rate":0.0003,"loan_rate":0.06,"margin_rate":0.5,"strategy_category":"index_enhanced"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), `"resolved_version"`) || strings.Contains(stdout.String(), `"resolved_template"`) {
		t.Fatalf("create output should not expose internal route metadata: %s", stdout.String())
	}
}

func TestInsertPaperTradingV1(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products":
			_, _ = w.Write([]byte(`{"products":[{"id":"p1","name":"prod"}]}`))
		case "/api/rqams/v2/products/paper_trading_channels:batch_upsert":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode returned error: %v", err)
			}
			ids, ok := body["product_ids"].([]any)
			if !ok || len(ids) != 1 || ids[0] != "p1" {
				t.Fatalf("unexpected product ids: %#v", body)
			}
			if body["futures_float_amount"] != nil {
				t.Fatalf("futures_float_amount should be cleared when futures_float_rate is set: %#v", body)
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"insert", "paper-trading", "--payload", `{"product_id_or_name":"prod","stock_min_fee":5,"futures_float_rate":0.1,"futures_float_amount":10}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), `"resolved_version"`) || strings.Contains(stdout.String(), `"resolved_template"`) || strings.Contains(stdout.String(), `"channel"`) {
		t.Fatalf("insert output should not expose internal route metadata: %s", stdout.String())
	}
}

func TestInsertPaperTradingSignalRoutesUploads(t *testing.T) {
	clearRQAMSCEnv(t)
	uploadPath := filepath.Join(t.TempDir(), "signals.csv")
	if err := os.WriteFile(uploadPath, []byte("date,order_book_id\n2026-01-01,000001.XSHE\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	var sawV1Upload bool
	var sawSessionUpload bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products:list_paper_trading_channels":
			_, _ = w.Write([]byte(`[{"_id":"c1","product_id":"p1"}]`))
		case "/api/rqams/v2/products:list_paper_trading_v2":
			_, _ = w.Write([]byte(`[{"product_id":"p2"}]`))
		case "/api/rqams/v2/products/p1/paper_trading_channels/c1/batch_paper_trading_file":
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				t.Fatalf("expected multipart upload")
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("ParseMultipartForm returned error: %v", err)
			}
			sawV1Upload = true
			_, _ = w.Write([]byte(`{"data":{"uploaded":1}}`))
		case "/api/rqams/v2/file_upload_sessions":
			_, _ = w.Write([]byte(`{"data":{"file_session_id":"fs1"}}`))
		case "/api/rqams/v2/file_upload_sessions/fs1/files":
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				t.Fatalf("expected multipart session upload")
			}
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Fatalf("Copy returned error: %v", err)
			}
			sawSessionUpload = true
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/api/rqams/v2/products/p2/paper_trading_v2/signals:batch_upload":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm returned error: %v", err)
			}
			if r.Form.Get("file_session_id") != "fs1" {
				t.Fatalf("unexpected form: %#v", r.Form)
			}
			_, _ = w.Write([]byte(`{"data":{"uploaded":1}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	for _, productID := range []string{"p1", "p2"} {
		var stdout strings.Builder
		code := Run(
			[]string{"insert", "paper-trading-signal", "--payload", `{"product_id":"` + productID + `","file_paths":["` + filepath.ToSlash(uploadPath) + `"]}`},
			strings.NewReader(""),
			&stdout,
			&strings.Builder{},
		)
		if code != 0 {
			t.Fatalf("Run insert for %s returned code %d: %s", productID, code, stdout.String())
		}
	}
	if !sawV1Upload || !sawSessionUpload {
		t.Fatalf("expected both upload paths, v1=%v session=%v", sawV1Upload, sawSessionUpload)
	}
}

func TestInsertPaperTradingSignalWaitsForHTTP202(t *testing.T) {
	clearRQAMSCEnv(t)
	uploadPath := filepath.Join(t.TempDir(), "signals.csv")
	if err := os.WriteFile(uploadPath, []byte("date,order_book_id\n2026-01-01,000001.XSHE\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	var uploadCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products:list_paper_trading_channels":
			_, _ = w.Write([]byte(`[{"_id":"c1","product_id":"p1"}]`))
		case "/api/rqams/v2/products:list_paper_trading_v2":
			_, _ = w.Write([]byte(`[]`))
		case "/api/rqams/v2/products/p1/paper_trading_channels/c1/batch_paper_trading_file":
			uploadCalls++
			if uploadCalls == 1 {
				if r.Method != "POST" {
					t.Fatalf("unexpected initial method: %s", r.Method)
				}
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"task_id":"pt-task"}`))
				return
			}
			if r.Method != "GET" {
				t.Fatalf("poll should use GET, got %s", r.Method)
			}
			if r.URL.Query().Get("task_id") != "pt-task" {
				t.Fatalf("unexpected query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":{"uploaded":1}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"insert", "paper-trading-signal", "--payload", `{"product_id":"p1","file_paths":["` + filepath.ToSlash(uploadPath) + `"]}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if uploadCalls != 2 {
		t.Fatalf("expected upload and poll calls, got %d", uploadCalls)
	}
	if !strings.Contains(stdout.String(), `"uploaded": 1`) {
		t.Fatalf("expected final async result: %s", stdout.String())
	}
}

func TestUseWorkspaceResolvesName(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/v1/workspaces" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"w1","name":"default"}]}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"use", "workspace", "--payload", `{"workspace_name_or_id":"default"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(raw), `"workspace_id": "w1"`) {
		t.Fatalf("workspace not saved: %s", string(raw))
	}
}

func TestUseWorkspaceUpdatesSelectedProfile(t *testing.T) {
	clearRQAMSCEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	rawConfig := `{
  "profile": "acct-a",
  "profiles": {
    "acct-a": {"base_url": "placeholder", "username": "a", "user_id": "ua", "sid": "sid-a", "workspace_id": "old-a"},
    "acct-b": {"base_url": "placeholder", "username": "b", "user_id": "ub", "sid": "sid-b", "workspace_id": "old-b"}
  }
}`
	if err := os.WriteFile(configPath, []byte(rawConfig), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sid, err := r.Cookie("sid")
		if err != nil || sid.Value != "sid-b" {
			t.Fatalf("expected selected profile sid-b, got %v", err)
		}
		if r.URL.Path != "/api/user/v1/workspaces" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"new-b","name":"target"}]}`))
	}))
	defer server.Close()

	// Patch both profile base URLs to the test server while keeping their
	// sessions distinct.
	rawConfig = strings.ReplaceAll(rawConfig, "placeholder", server.URL)
	if err := os.WriteFile(configPath, []byte(rawConfig), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run(
		[]string{"use", "workspace", "--payload", `{"profile":"acct-b","workspace_name_or_id":"target"}`},
		strings.NewReader(""),
		&stdout,
		&strings.Builder{},
	)
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("saved config should be JSON: %v", err)
	}
	profiles := saved["profiles"].(map[string]any)
	profileA := profiles["acct-a"].(map[string]any)
	profileB := profiles["acct-b"].(map[string]any)
	if profileA["workspace_id"] != "old-a" {
		t.Fatalf("acct-a workspace should remain isolated: %#v", profileA)
	}
	if profileB["workspace_id"] != "new-b" {
		t.Fatalf("acct-b workspace was not updated: %#v", profileB)
	}
}

func TestBusinessCommandSelectsProfileFromPayload(t *testing.T) {
	clearRQAMSCEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rqams/v2/products" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("profile") != "" {
			t.Fatalf("profile should not be forwarded as query: %s", r.URL.RawQuery)
		}
		sid, err := r.Cookie("sid")
		if err != nil || sid.Value != "sid-b" {
			t.Fatalf("expected selected profile sid-b, got %v", err)
		}
		if r.Header.Get("X-AMS-Workspace") != "w-b" {
			t.Fatalf("expected selected profile workspace w-b, got %s", r.Header.Get("X-AMS-Workspace"))
		}
		_, _ = w.Write([]byte(`{"products":[]}`))
	}))
	defer server.Close()
	rawConfig := `{
  "profiles": {
    "acct-a": {"base_url": ` + quote(server.URL) + `, "username": "a", "user_id": "ua", "sid": "sid-a", "workspace_id": "w-a"},
    "acct-b": {"base_url": ` + quote(server.URL) + `, "username": "b", "user_id": "ub", "sid": "sid-b", "workspace_id": "w-b"}
  }
}`
	if err := os.WriteFile(configPath, []byte(rawConfig), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "product-list", "--payload", `{"profile":"acct-b"}`}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
}

func TestBusinessCommandRefreshesExpiredSessionFromStoredPassword(t *testing.T) {
	clearRQAMSCEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	var productCalls int
	var loginCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rqams/v2/products":
			productCalls++
			sid, _ := r.Cookie("sid")
			if productCalls == 1 {
				if sid == nil || sid.Value != "expired-sid" {
					t.Fatalf("expected initial expired sid, got %#v", sid)
				}
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"expired"}`))
				return
			}
			if sid == nil || sid.Value != "fresh-sid" {
				t.Fatalf("expected refreshed sid, got %#v", sid)
			}
			if r.Header.Get("X-AMS-User") != "fresh-user-id" {
				t.Fatalf("expected refreshed user header, got %s", r.Header.Get("X-AMS-User"))
			}
			_, _ = w.Write([]byte(`{"products":[]}`))
		case "/api/user/login":
			loginCalls++
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm returned error: %v", err)
			}
			if r.Form.Get("username") != "stored-user" || r.Form.Get("password") != "stored-pass" {
				t.Fatalf("unexpected login form: %#v", r.Form)
			}
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "fresh-sid"})
			_, _ = w.Write([]byte(`{"code":0,"data":{"user_id":"fresh-user-id"}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	rawConfig := `{"base_url":` + quote(server.URL) + `,"username":"stored-user","password":"stored-pass","user_id":"old-user-id","sid":"expired-sid","workspace_id":"w1"}`
	if err := os.WriteFile(configPath, []byte(rawConfig), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "product-list", "--payload", "{}"}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if loginCalls != 1 || productCalls != 2 {
		t.Fatalf("expected login once and product retry, login=%d product=%d", loginCalls, productCalls)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(raw), `"sid": "fresh-sid"`) || !strings.Contains(string(raw), `"password": "stored-pass"`) {
		t.Fatalf("config should persist refreshed session and password: %s", string(raw))
	}
}

func TestGetCurrentWorkspaceReturnsNameAndID(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/v1/workspaces" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"w1","name":"榛樿绌洪棿"}]}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "w1")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "current-workspace", "--payload", "{}"}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"workspace_name": "榛樿绌洪棿"`) {
		t.Fatalf("workspace name missing: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"display": "榛樿绌洪棿 (w1)"`) {
		t.Fatalf("workspace display missing: %s", stdout.String())
	}
}

func TestGetCurrentWorkspaceReturnsDefaultNameAndID(t *testing.T) {
	clearRQAMSCEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/v1/workspaces" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"w1","name":"榛樿绌洪棿"}]}`))
	}))
	defer server.Close()

	configPath := writeConfig(t, server.URL, "")
	t.Setenv("RQAMS_CLI_CONFIG", configPath)

	var stdout strings.Builder
	code := Run([]string{"get", "current-workspace", "--payload", "{}"}, strings.NewReader(""), &stdout, &strings.Builder{})
	if code != 0 {
		t.Fatalf("Run returned code %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), `"display": "榛樿绌洪棿 (w1)"`) {
		t.Fatalf("workspace display missing: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"defaulted": true`) {
		t.Fatalf("default marker missing: %s", stdout.String())
	}
}

func writeConfig(t *testing.T, baseURL string, workspaceID string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"base_url":` + quote(baseURL) + `,"user_id":"u1","sid":"s1","workspace_id":` + quote(workspaceID) + `}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return path
}

func clearRQAMSCEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"RQAMS_TEST_PRODUCT_LIST_TOTAL",
	} {
		t.Setenv(name, "")
	}
}

func quote(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
