package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"rqams-cli/internal/config"
)

func TestAMSRequestPollsHTTP202Task(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-AMS-Workspace") != "w1" {
			t.Fatalf("missing workspace header")
		}
		switch calls {
		case 1:
			if r.Method != "POST" || r.URL.Path != "/api/rqams/v2/products/p1:async_op" {
				t.Fatalf("unexpected initial request: %s %s", r.Method, r.URL.String())
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "task1"})
		case 2:
			if r.Method != "GET" || r.URL.Path != "/api/rqams/v2/products/p1:async_op" {
				t.Fatalf("unexpected poll request: %s %s", r.Method, r.URL.String())
			}
			if r.URL.Query().Get("task_id") != "task1" {
				t.Fatalf("missing task_id query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "SUCCESS", "data": map[string]any{"done": true}})
		default:
			t.Fatalf("unexpected extra call %d", calls)
		}
	}))
	defer server.Close()

	data, err := New(testConfig(server.URL)).AMSRequest("POST", "products/p1:async_op", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("AMSRequest returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected two calls, got %d", calls)
	}
	root, ok := data.(map[string]any)
	if !ok || root["status"] != "SUCCESS" {
		t.Fatalf("unexpected data: %#v", data)
	}
}

func TestAMSRequestWithParamsPollsHTTP202TaskWithOriginalQuery(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			if r.Method != "GET" || r.URL.Path != "/api/rqams/v2/product_group_overview/indicators_v2" {
				t.Fatalf("unexpected initial request: %s %s", r.Method, r.URL.String())
			}
			if r.URL.Query().Get("product_or_group_ids") != "p1" || r.URL.Query().Get("task_id") != "" {
				t.Fatalf("unexpected initial query: %s", r.URL.RawQuery)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "task1"})
		case 2:
			if r.Method != "GET" || r.URL.Path != "/api/rqams/v2/product_group_overview/indicators_v2" {
				t.Fatalf("unexpected poll request: %s %s", r.Method, r.URL.String())
			}
			if r.URL.Query().Get("product_or_group_ids") != "p1" {
				t.Fatalf("poll lost original query: %s", r.URL.RawQuery)
			}
			if r.URL.Query().Get("task_id") != "task1" {
				t.Fatalf("poll lost task_id query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "SUCCESS", "data": []any{map[string]any{"id": "p1"}}})
		default:
			t.Fatalf("unexpected extra call %d", calls)
		}
	}))
	defer server.Close()

	params := url.Values{}
	params.Set("product_or_group_ids", "p1")
	params.Set("start_date", "2026-01-01")
	params.Set("end_date", "2026-01-31")
	data, err := New(testConfig(server.URL)).AMSRequestWithParams("GET", "product_group_overview/indicators_v2", nil, params)
	if err != nil {
		t.Fatalf("AMSRequestWithParams returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected two calls, got %d", calls)
	}
	root, ok := data.(map[string]any)
	if !ok || root["status"] != "SUCCESS" {
		t.Fatalf("unexpected data: %#v", data)
	}
}

func TestAMSMultipartRequestPollsWithoutUploadBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upload.csv")
	if err := os.WriteFile(path, []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			if r.Method != "POST" {
				t.Fatalf("unexpected initial method: %s", r.Method)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("ParseMultipartForm returned error: %v", err)
			}
			if _, _, err := r.FormFile("file"); err != nil {
				t.Fatalf("missing multipart file field: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "upload-task"})
		case 2:
			if r.Method != "GET" {
				t.Fatalf("poll should use GET, got %s", r.Method)
			}
			if r.Header.Get("Content-Type") != "" {
				t.Fatalf("poll should not keep multipart content type: %s", r.Header.Get("Content-Type"))
			}
			if r.ContentLength > 0 {
				t.Fatalf("poll should not keep upload body, content length %d", r.ContentLength)
			}
			if r.URL.Query().Get("task_id") != "upload-task" {
				t.Fatalf("missing task_id query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "SUCCESS"})
		default:
			t.Fatalf("unexpected extra call %d", calls)
		}
	}))
	defer server.Close()

	_, err := New(testConfig(server.URL)).AMSMultipartRequest("POST", "products/p1:upload", nil, []UploadFile{
		{FieldName: "file", Path: path},
	})
	if err != nil {
		t.Fatalf("AMSMultipartRequest returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected two calls, got %d", calls)
	}
}

func testConfig(baseURL string) config.Config {
	return config.Config{
		BaseURL:     baseURL,
		WorkspaceID: "w1",
		UserID:      "u1",
		SID:         "s1",
	}
}
