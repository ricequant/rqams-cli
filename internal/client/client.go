package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rqams-cli/internal/asyncpoll"
	"rqams-cli/internal/config"
)

// HTTPError preserves non-2xx response details.
type HTTPError struct {
	Method string
	URL    string
	Status int
	Body   string
}

// Error formats an HTTP error for CLI output.
func (err HTTPError) Error() string {
	return fmt.Sprintf("%s %s failed with HTTP %d: %s", err.Method, err.URL, err.Status, err.Body)
}

// Client is a small rqamsc HTTP client compatible with the Python SDK URL layout.
type Client struct {
	cfg        config.Config
	httpClient *http.Client
}

// LoginResponse captures the fields needed for local auth state.
type LoginResponse struct {
	UserID string
	SID    string
	Data   any
}

// DownloadResult captures a file download or JSON response returned by AMS.
type DownloadResult struct {
	Path        string
	ContentType string
	Bytes       int
	Data        any
}

// UploadFile describes one local file to send as multipart/form-data.
type UploadFile struct {
	FieldName string
	Path      string
	FileName  string
}

// New creates a Client.
func New(cfg config.Config) Client {
	return Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// UserRequest calls /api/user endpoints.
func (c Client) UserRequest(method string, path string, body any) (any, error) {
	baseURL, err := c.cfg.RequireBaseURL()
	if err != nil {
		return nil, err
	}
	return c.request(method, joinURL(baseURL, "/api/user/", path), body, requestOptions{})
}

// Login calls the Python SDK-compatible /api/user/login endpoint.
func (c Client) Login(username string, password string) (LoginResponse, error) {
	baseURL, err := c.cfg.RequireBaseURL()
	if err != nil {
		return LoginResponse{}, err
	}
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)
	form.Set("rememberMe", "false")
	req, err := http.NewRequest(
		"POST",
		joinURL(baseURL, "/api/user/", "login"),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return LoginResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rsp, err := c.httpClient.Do(req)
	if err != nil {
		return LoginResponse{}, err
	}
	defer rsp.Body.Close()
	raw, err := io.ReadAll(rsp.Body)
	if err != nil {
		return LoginResponse{}, err
	}
	if rsp.StatusCode < 200 || rsp.StatusCode >= 300 {
		return LoginResponse{}, HTTPError{Method: "POST", URL: req.URL.String(), Status: rsp.StatusCode, Body: string(raw)}
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return LoginResponse{}, fmt.Errorf("login response is not valid JSON: %w", err)
	}
	if code, ok := data["code"].(float64); ok && code != 0 {
		return LoginResponse{}, fmt.Errorf("login failed: %v", data["message"])
	}
	sid := ""
	for _, cookie := range rsp.Cookies() {
		if cookie.Name == "sid" {
			sid = cookie.Value
			break
		}
	}
	if sid == "" {
		return LoginResponse{}, fmt.Errorf("login response did not include sid cookie")
	}
	userID := findUserID(data)
	if userID == "" {
		userID = c.cfg.UserID
	}
	if userID == "" {
		return LoginResponse{}, fmt.Errorf("login response did not include userId")
	}
	return LoginResponse{UserID: userID, SID: sid, Data: data}, nil
}

func findUserID(data map[string]any) string {
	for _, field := range []string{"userId", "user_id", "id"} {
		if value := stringify(data[field]); value != "" {
			return value
		}
	}
	for _, field := range []string{"data", "user"} {
		nested, ok := data[field].(map[string]any)
		if !ok {
			continue
		}
		if value := findUserID(nested); value != "" {
			return value
		}
	}
	return ""
}

func stringify(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return fmt.Sprintf("%.0f", typed)
	default:
		return ""
	}
}

// AMSRequest calls /api/rqams/v2 endpoints.
func (c Client) AMSRequest(method string, path string, body any) (any, error) {
	return c.AMSRequestWithParams(method, path, body, nil)
}

// AMSRequestWithParams calls /api/rqams/v2 endpoints with query parameters.
func (c Client) AMSRequestWithParams(method string, path string, body any, params url.Values) (any, error) {
	baseURL, err := c.cfg.RequireBaseURL()
	if err != nil {
		return nil, err
	}
	workspaceID, err := c.workspaceID()
	if err != nil {
		return nil, err
	}
	return c.request(method, joinURL(baseURL, "/api/rqams/v2/", path), body, requestOptions{
		ams:         true,
		workspaceID: workspaceID,
		query:       params,
	})
}

// AMSFormRequest calls /api/rqams/v2 endpoints with x-www-form-urlencoded data.
func (c Client) AMSFormRequest(method string, path string, fields map[string]string) (any, error) {
	values := url.Values{}
	for key, value := range fields {
		values.Set(key, value)
	}
	return c.amsRequestWithReader(method, path, strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", nil)
}

// AMSMultipartRequest calls /api/rqams/v2 endpoints with multipart/form-data.
func (c Client) AMSMultipartRequest(method string, path string, fields map[string]string, files []UploadFile) (any, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, err
		}
	}
	for _, upload := range files {
		fieldName := strings.TrimSpace(upload.FieldName)
		if fieldName == "" {
			fieldName = "files"
		}
		fileName := strings.TrimSpace(upload.FileName)
		if fileName == "" {
			fileName = filepath.Base(upload.Path)
		}
		part, err := writer.CreateFormFile(fieldName, fileName)
		if err != nil {
			return nil, err
		}
		file, err := os.Open(upload.Path)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(part, file); err != nil {
			_ = file.Close()
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return c.amsRequestWithReader(method, path, body, writer.FormDataContentType(), nil)
}

// AMSDownloadToFile calls /api/rqams/v2 and writes non-JSON content to destination.
func (c Client) AMSDownloadToFile(path string, params url.Values, destination string) (DownloadResult, error) {
	baseURL, err := c.cfg.RequireBaseURL()
	if err != nil {
		return DownloadResult{}, err
	}
	workspaceID, err := c.workspaceID()
	if err != nil {
		return DownloadResult{}, err
	}
	return c.downloadToFile(joinURL(baseURL, "/api/rqams/v2/", path), params, destination, requestOptions{
		ams:         true,
		workspaceID: workspaceID,
	})
}

func (c Client) amsRequestWithReader(method string, path string, reader io.Reader, contentType string, params url.Values) (any, error) {
	baseURL, err := c.cfg.RequireBaseURL()
	if err != nil {
		return nil, err
	}
	workspaceID, err := c.workspaceID()
	if err != nil {
		return nil, err
	}
	return c.requestWithReader(method, joinURL(baseURL, "/api/rqams/v2/", path), reader, contentType, requestOptions{
		ams:         true,
		workspaceID: workspaceID,
		query:       params,
	})
}

func (c Client) downloadToFile(requestURL string, params url.Values, destination string, options requestOptions) (DownloadResult, error) {
	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return DownloadResult{}, err
	}
	if len(params) > 0 {
		req.URL.RawQuery = params.Encode()
	}
	if c.cfg.SID != "" {
		req.AddCookie(&http.Cookie{Name: "sid", Value: c.cfg.SID})
	}
	if options.ams {
		req.Header.Set("X-AMS-Workspace", options.workspaceID)
		if c.cfg.UserID != "" {
			req.Header.Set("X-AMS-User", c.cfg.UserID)
		}
	}
	rsp, err := c.httpClient.Do(req)
	if err != nil {
		return DownloadResult{}, err
	}
	defer rsp.Body.Close()
	raw, err := io.ReadAll(rsp.Body)
	if err != nil {
		return DownloadResult{}, err
	}
	if rsp.StatusCode < 200 || rsp.StatusCode >= 300 {
		return DownloadResult{}, HTTPError{Method: "GET", URL: req.URL.String(), Status: rsp.StatusCode, Body: string(raw)}
	}
	contentType := rsp.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var data any
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &data); err != nil {
				return DownloadResult{}, fmt.Errorf("download response is not valid JSON: %w", err)
			}
		}
		if root, ok := data.(map[string]any); ok {
			if taskID := stringify(root["task_id"]); taskID != "" {
				return c.pollDownloadTask(req.URL.String(), taskID, destination, options)
			}
			if stringify(root["status"]) == "DOING" {
				return c.pollDownloadTask(req.URL.String(), "", destination, options)
			}
		}
		return DownloadResult{ContentType: contentType, Data: data}, nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return DownloadResult{}, err
	}
	if err := os.WriteFile(destination, raw, 0o644); err != nil {
		return DownloadResult{}, err
	}
	return DownloadResult{Path: destination, ContentType: contentType, Bytes: len(raw)}, nil
}

func (c Client) pollDownloadTask(requestURL string, taskID string, destination string, options requestOptions) (DownloadResult, error) {
	if options.asyncPolls >= asyncpoll.MaxAttempts {
		return DownloadResult{}, fmt.Errorf("GET %s async download did not finish before timeout", requestURL)
	}
	if taskID != "" {
		parsedURL, err := url.Parse(requestURL)
		if err != nil {
			return DownloadResult{}, err
		}
		query := parsedURL.Query()
		query.Set("task_id", taskID)
		parsedURL.RawQuery = query.Encode()
		requestURL = parsedURL.String()
	}
	time.Sleep(asyncpoll.Interval)
	next := options
	next.query = nil
	next.asyncPolls++
	return c.downloadToFile(requestURL, nil, destination, next)
}

// Workspaces returns the raw workspace list from /api/user/v1/workspaces.
func (c Client) Workspaces() ([]map[string]any, error) {
	data, err := c.UserRequest("GET", "v1/workspaces", nil)
	if err != nil {
		return nil, err
	}
	root, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("workspace response must be an object")
	}
	items, ok := root["data"].([]any)
	if !ok {
		return nil, fmt.Errorf("workspace response missing data list")
	}
	workspaces := make([]map[string]any, 0, len(items))
	for _, item := range items {
		workspace, ok := item.(map[string]any)
		if !ok {
			continue
		}
		workspaces = append(workspaces, workspace)
	}
	return workspaces, nil
}

func (c Client) workspaceID() (string, error) {
	if c.cfg.WorkspaceID != "" {
		return c.cfg.WorkspaceID, nil
	}
	workspaces, err := c.Workspaces()
	if err != nil {
		return "", err
	}
	if len(workspaces) == 0 {
		return "", fmt.Errorf("missing workspace; run use workspace")
	}
	id, ok := workspaces[0]["id"].(string)
	if !ok || id == "" {
		return "", fmt.Errorf("first workspace is missing id")
	}
	return id, nil
}

type requestOptions struct {
	ams         bool
	workspaceID string
	query       url.Values
	asyncPolls  int
}

func (c Client) request(method string, url string, body any, options requestOptions) (any, error) {
	var reader io.Reader
	contentType := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
		contentType = "application/json"
	}
	return c.requestWithReader(method, url, reader, contentType, options)
}

func (c Client) requestWithReader(method string, url string, reader io.Reader, contentType string, options requestOptions) (any, error) {
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, err
	}
	if len(options.query) > 0 {
		req.URL.RawQuery = options.query.Encode()
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.cfg.SID != "" {
		req.AddCookie(&http.Cookie{Name: "sid", Value: c.cfg.SID})
	}
	if options.ams {
		req.Header.Set("X-AMS-Workspace", options.workspaceID)
		if c.cfg.UserID != "" {
			req.Header.Set("X-AMS-User", c.cfg.UserID)
		}
	}

	rsp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer rsp.Body.Close()
	raw, err := io.ReadAll(rsp.Body)
	if err != nil {
		return nil, err
	}
	if rsp.StatusCode == http.StatusAccepted {
		return c.pollAsyncTask(method, req.URL.String(), raw, options)
	}
	if rsp.StatusCode < 200 || rsp.StatusCode >= 300 {
		return nil, HTTPError{Method: method, URL: url, Status: rsp.StatusCode, Body: string(raw)}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("response is not valid JSON: %w", err)
	}
	return data, nil
}

func (c Client) pollAsyncTask(method string, requestURL string, raw []byte, options requestOptions) (any, error) {
	if options.asyncPolls >= asyncpoll.MaxAttempts {
		return nil, fmt.Errorf("%s %s async task did not finish before timeout", method, requestURL)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("async task response is not valid JSON: %w", err)
	}
	taskID := stringify(data["task_id"])
	if taskID == "" {
		time.Sleep(asyncpoll.Interval)
		next := options
		next.query = nil
		next.asyncPolls++
		return c.requestWithReader(method, requestURL, nil, "", next)
	}
	parsedURL, err := url.Parse(requestURL)
	if err != nil {
		return nil, err
	}
	query := parsedURL.Query()
	query.Set("task_id", taskID)
	parsedURL.RawQuery = query.Encode()
	time.Sleep(asyncpoll.Interval)
	next := options
	next.query = nil
	next.asyncPolls++
	return c.requestWithReader("GET", parsedURL.String(), nil, "", next)
}

func joinURL(base string, prefix string, path string) string {
	base = strings.TrimRight(base, "/")
	prefix = strings.Trim(prefix, "/")
	path = strings.TrimLeft(path, "/")
	joined, err := url.JoinPath(base, prefix, path)
	if err != nil {
		return base + "/" + prefix + "/" + path
	}
	return joined
}
