package cmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itchyny/gojq"
)

// --- buildAPIBody ---

func TestBuildAPIBody_NoBodyGET(t *testing.T) {
	body, err := buildAPIBody(apiBodyParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.method != "GET" {
		t.Errorf("expected GET, got %s", body.method)
	}
	if body.data != nil {
		t.Errorf("expected nil data, got %v", body.data)
	}
}

func TestBuildAPIBody_ExplicitMethod(t *testing.T) {
	body, err := buildAPIBody(apiBodyParams{method: "DELETE"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.method != "DELETE" {
		t.Errorf("expected DELETE, got %s", body.method)
	}
}

func TestBuildAPIBody_FieldsAutoPost(t *testing.T) {
	body, err := buildAPIBody(apiBodyParams{
		fields: []string{"name=test", "chat_type=group"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.method != "POST" {
		t.Errorf("expected POST, got %s", body.method)
	}
	if body.contentType != "application/json" {
		t.Errorf("expected application/json, got %s", body.contentType)
	}

	var obj map[string]any
	if err := json.Unmarshal(body.data, &obj); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if obj["name"] != "test" || obj["chat_type"] != "group" {
		t.Errorf("unexpected JSON body: %s", string(body.data))
	}
}

func TestBuildAPIBody_InvalidFieldFormat(t *testing.T) {
	_, err := buildAPIBody(apiBodyParams{
		fields: []string{"invalid-no-equals"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid field format") {
		t.Errorf("expected invalid field format error, got: %v", err)
	}
}

func TestBuildAPIBody_RawMode(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "body.json")
	os.WriteFile(path, []byte(`{"raw":"data"}`), 0644)

	body, err := buildAPIBody(apiBodyParams{
		inputFile: path,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.method != "POST" {
		t.Errorf("expected POST, got %s", body.method)
	}
	if body.contentType != "" {
		t.Errorf("expected empty content type in raw mode, got %s", body.contentType)
	}
	if string(body.data) != `{"raw":"data"}` {
		t.Errorf("unexpected data: %s", string(body.data))
	}
}

func TestBuildAPIBody_RawModeStdin(t *testing.T) {
	body, err := buildAPIBody(apiBodyParams{
		inputFile: "-",
		stdin:     strings.NewReader("stdin data"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body.data) != "stdin data" {
		t.Errorf("expected 'stdin data', got %q", string(body.data))
	}
}

func TestBuildAPIBody_InputAndFieldsMutualExclusion(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "body.json")
	os.WriteFile(path, []byte("data"), 0644)

	_, err := buildAPIBody(apiBodyParams{
		inputFile: path,
		fields:    []string{"key=val"},
	})
	if err == nil || !strings.Contains(err.Error(), "--input and -f/-F are mutually exclusive") {
		t.Errorf("expected mutual exclusion error, got: %v", err)
	}
}

// --- buildAPIBody with -F typed fields ---

func TestBuildAPIBody_TypedFieldsBool(t *testing.T) {
	body, err := buildAPIBody(apiBodyParams{
		typedFields: []string{"active=true", "deleted=false"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.contentType != "application/json" {
		t.Errorf("expected application/json, got %s", body.contentType)
	}
	if body.method != "POST" {
		t.Errorf("expected POST, got %s", body.method)
	}

	var obj map[string]any
	if err := json.Unmarshal(body.data, &obj); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if obj["active"] != true {
		t.Errorf("expected active=true (bool), got %v (%T)", obj["active"], obj["active"])
	}
	if obj["deleted"] != false {
		t.Errorf("expected deleted=false (bool), got %v (%T)", obj["deleted"], obj["deleted"])
	}
}

func TestBuildAPIBody_TypedFieldsNumber(t *testing.T) {
	body, err := buildAPIBody(apiBodyParams{
		typedFields: []string{"count=42", "negative=-5", "zero=0"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(body.data, &obj); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// JSON numbers unmarshal as float64
	if obj["count"] != float64(42) {
		t.Errorf("expected count=42, got %v (%T)", obj["count"], obj["count"])
	}
	if obj["negative"] != float64(-5) {
		t.Errorf("expected negative=-5, got %v (%T)", obj["negative"], obj["negative"])
	}
	if obj["zero"] != float64(0) {
		t.Errorf("expected zero=0, got %v (%T)", obj["zero"], obj["zero"])
	}
}

func TestBuildAPIBody_TypedFieldsFileRef(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "content.txt")
	os.WriteFile(path, []byte("file contents here"), 0644)

	body, err := buildAPIBody(apiBodyParams{
		typedFields: []string{"data=@" + path},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(body.data, &obj); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if obj["data"] != "file contents here" {
		t.Errorf("expected file contents, got %v", obj["data"])
	}
}

func TestBuildAPIBody_TypedFieldsString(t *testing.T) {
	body, err := buildAPIBody(apiBodyParams{
		typedFields: []string{"name=hello", "value=3.14"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(body.data, &obj); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if obj["name"] != "hello" {
		t.Errorf("expected name=hello (string), got %v (%T)", obj["name"], obj["name"])
	}
	// 3.14 is not an integer, so should remain string
	if obj["value"] != "3.14" {
		t.Errorf("expected value=3.14 (string), got %v (%T)", obj["value"], obj["value"])
	}
}

func TestBuildAPIBody_MixedFieldsAndTypedFields(t *testing.T) {
	body, err := buildAPIBody(apiBodyParams{
		fields:      []string{"name=test"},
		typedFields: []string{"count=5", "active=true"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(body.data, &obj); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if obj["name"] != "test" {
		t.Errorf("expected name=test (string), got %v", obj["name"])
	}
	if obj["count"] != float64(5) {
		t.Errorf("expected count=5 (number), got %v (%T)", obj["count"], obj["count"])
	}
	if obj["active"] != true {
		t.Errorf("expected active=true (bool), got %v (%T)", obj["active"], obj["active"])
	}
}

func TestBuildAPIBody_TypedFieldsFileNotFound(t *testing.T) {
	_, err := buildAPIBody(apiBodyParams{
		typedFields: []string{"data=@/nonexistent/file.txt"},
	})
	if err == nil || !strings.Contains(err.Error(), "reading file") {
		t.Errorf("expected file read error, got: %v", err)
	}
}

func TestBuildAPIBody_TypedFieldsInvalidFormat(t *testing.T) {
	_, err := buildAPIBody(apiBodyParams{
		typedFields: []string{"no-equals-sign"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid field format") {
		t.Errorf("expected invalid field format error, got: %v", err)
	}
}

// --- hasAuthHeader ---

func TestHasAuthHeader(t *testing.T) {
	tests := []struct {
		headers []string
		want    bool
	}{
		{nil, false},
		{[]string{"Content-Type:application/json"}, false},
		{[]string{"Authorization:Bearer token"}, true},
		{[]string{"authorization:Bearer token"}, true},
		{[]string{"X-Custom:val", "Authorization:tok"}, true},
	}
	for _, tt := range tests {
		got := hasAuthHeader(tt.headers)
		if got != tt.want {
			t.Errorf("hasAuthHeader(%v) = %v, want %v", tt.headers, got, tt.want)
		}
	}
}

// --- buildHTTPRequest ---

func TestBuildHTTPRequest_BasicGET(t *testing.T) {
	body := &apiBody{method: "GET"}
	req, err := buildHTTPRequest(t.Context(), body, "/api/v3/test", "https://example.com", "mytoken", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Method != "GET" {
		t.Errorf("expected GET, got %s", req.Method)
	}
	if req.URL.String() != "https://example.com/api/v3/test" {
		t.Errorf("unexpected URL: %s", req.URL.String())
	}
	if req.Header.Get("Authorization") != "Bearer mytoken" {
		t.Errorf("unexpected Authorization: %s", req.Header.Get("Authorization"))
	}
}

func TestBuildHTTPRequest_CustomHeaders(t *testing.T) {
	body := &apiBody{method: "GET", contentType: "application/json"}
	req, err := buildHTTPRequest(t.Context(), body, "/test", "https://host", "tok", []string{
		"Content-Type:text/plain",
		"X-Custom:value",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Custom header overrides Content-Type from body
	if req.Header.Get("Content-Type") != "text/plain" {
		t.Errorf("expected text/plain, got %s", req.Header.Get("Content-Type"))
	}
	if req.Header.Get("X-Custom") != "value" {
		t.Errorf("expected value, got %s", req.Header.Get("X-Custom"))
	}
}

func TestBuildHTTPRequest_ManualAuth(t *testing.T) {
	body := &apiBody{method: "GET"}
	headers := []string{"Authorization:token mytoken"}
	req, err := buildHTTPRequest(t.Context(), body, "/test", "https://host", "auto-tok", headers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// User-provided auth should be used, not auto token
	if req.Header.Get("Authorization") != "token mytoken" {
		t.Errorf("expected user Authorization, got %s", req.Header.Get("Authorization"))
	}
}

// --- runApi with httptest ---

func apiTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func apiTestConfig(t *testing.T, host string) string {
	t.Helper()
	return writeTestConfig(t, fmt.Sprintf(`
bots:
  default:
    host: %s
    id: test-bot-id
    token: test-token
`, host))
}

func TestRunApi_GETRequest(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("missing Authorization header")
		}
		w.WriteHeader(200)
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	cfgPath := apiTestConfig(t, ts.URL)
	deps, stdout, _ := testDeps()

	err := runApi([]string{"--config", cfgPath, "/api/v3/test"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), `{"status":"ok"}`) {
		t.Errorf("unexpected output: %s", stdout.String())
	}
}

func TestRunApi_POSTWithJSONBody(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %s", ct)
		}
		body, _ := io.ReadAll(r.Body)
		var obj map[string]any
		if err := json.Unmarshal(body, &obj); err != nil {
			t.Errorf("invalid JSON body: %v", err)
		}
		if obj["name"] != "test" {
			t.Errorf("expected name=test, got %v", obj["name"])
		}
		fmt.Fprint(w, `{"result":"created"}`)
	})

	cfgPath := apiTestConfig(t, ts.URL)
	deps, stdout, _ := testDeps()

	err := runApi([]string{
		"--config", cfgPath,
		"-X", "POST",
		"-f", "name=test",
		"-f", "chat_type=group",
		"/api/v3/chats/create",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "created") {
		t.Errorf("unexpected output: %s", stdout.String())
	}
}

func TestRunApi_IncludeHeaders(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "response-val")
		fmt.Fprint(w, "body")
	})

	cfgPath := apiTestConfig(t, ts.URL)
	deps, stdout, _ := testDeps()

	err := runApi([]string{"--config", cfgPath, "-i", "/api/v3/test"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "200") {
		t.Errorf("expected status 200 in output: %s", out)
	}
	if !strings.Contains(out, "X-Custom: response-val") {
		t.Errorf("expected X-Custom header in output: %s", out)
	}
	if !strings.Contains(out, "body") {
		t.Errorf("expected body in output: %s", out)
	}
}

func TestRunApi_SilentMode(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "should not appear")
	})

	cfgPath := apiTestConfig(t, ts.URL)
	deps, stdout, _ := testDeps()

	err := runApi([]string{"--config", cfgPath, "--silent", "/api/v3/test"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout.String() != "" {
		t.Errorf("expected empty output with --silent, got: %s", stdout.String())
	}
}

func TestRunApi_NonSuccessExitError(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":"not found"}`)
	})

	cfgPath := apiTestConfig(t, ts.URL)
	deps, stdout, _ := testDeps()

	err := runApi([]string{"--config", cfgPath, "/api/v3/missing"}, deps)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got: %v", err)
	}
	if exitErr.Code != 1 {
		t.Errorf("expected code 1, got %d", exitErr.Code)
	}
	// Body should still be in stdout
	if !strings.Contains(stdout.String(), "not found") {
		t.Errorf("expected error body in stdout: %s", stdout.String())
	}
}

func TestRunApi_BinaryResponse(t *testing.T) {
	binaryData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG header
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(binaryData)
	})

	cfgPath := apiTestConfig(t, ts.URL)
	deps, stdout, _ := testDeps()

	err := runApi([]string{"--config", cfgPath, "/api/v3/files/download"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(stdout.Bytes()) != string(binaryData) {
		t.Errorf("binary data mismatch")
	}
}

func TestRunApi_CustomHeader(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom") != "my-value" {
			t.Errorf("expected X-Custom=my-value, got %s", r.Header.Get("X-Custom"))
		}
		fmt.Fprint(w, "ok")
	})

	cfgPath := apiTestConfig(t, ts.URL)
	deps, _, _ := testDeps()

	err := runApi([]string{"--config", cfgPath, "-H", "X-Custom:my-value", "/api/v3/test"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunApi_PrettyPrintJSON(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"a":1,"b":2}`)
	})

	cfgPath := apiTestConfig(t, ts.URL)
	deps, stdout, _ := testDeps()

	err := runApi([]string{"--config", cfgPath, "--format", "json", "/api/v3/test"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "  \"a\": 1") {
		t.Errorf("expected pretty-printed JSON, got: %s", out)
	}
}

func TestRunApi_RawInput(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != "<xml>data</xml>" {
			t.Errorf("expected raw XML body, got: %s", string(body))
		}
		if r.Header.Get("Content-Type") != "application/xml" {
			t.Errorf("expected application/xml, got %s", r.Header.Get("Content-Type"))
		}
		fmt.Fprint(w, "ok")
	})

	tmp := t.TempDir()
	inputPath := filepath.Join(tmp, "body.xml")
	os.WriteFile(inputPath, []byte("<xml>data</xml>"), 0644)

	cfgPath := apiTestConfig(t, ts.URL)
	deps, _, _ := testDeps()

	err := runApi([]string{
		"--config", cfgPath,
		"-X", "POST",
		"--input", inputPath,
		"-H", "Content-Type:application/xml",
		"/api/v3/smartapps/event",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunApi_TypedFieldsPOST(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var obj map[string]any
		if err := json.Unmarshal(body, &obj); err != nil {
			t.Fatalf("invalid JSON body: %v", err)
		}
		// -f name=test → string
		if obj["name"] != "test" {
			t.Errorf("expected name=test, got %v", obj["name"])
		}
		// -F count=10 → number
		if obj["count"] != float64(10) {
			t.Errorf("expected count=10 (number), got %v (%T)", obj["count"], obj["count"])
		}
		// -F active=true → bool
		if obj["active"] != true {
			t.Errorf("expected active=true (bool), got %v (%T)", obj["active"], obj["active"])
		}
		fmt.Fprint(w, `{"result":"ok"}`)
	})

	cfgPath := apiTestConfig(t, ts.URL)
	deps, stdout, _ := testDeps()

	err := runApi([]string{
		"--config", cfgPath,
		"-f", "name=test",
		"-F", "count=10",
		"-F", "active=true",
		"/api/v3/test",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "ok") {
		t.Errorf("unexpected output: %s", stdout.String())
	}
}

func TestRunApi_Timeout(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response — but test just checks the request goes through
		fmt.Fprint(w, "ok")
	})

	cfgPath := apiTestConfig(t, ts.URL)
	deps, _, _ := testDeps()

	err := runApi([]string{"--config", cfgPath, "--timeout", "5s", "/api/v3/test"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Validation errors ---

func TestRunApi_MissingEndpoint(t *testing.T) {
	deps, _, _ := testDeps()
	err := runApi([]string{}, deps)
	if err == nil || !strings.Contains(err.Error(), "endpoint required") {
		t.Errorf("expected 'endpoint required', got: %v", err)
	}
}

func TestRunApi_EndpointMustStartWithSlash(t *testing.T) {
	deps, _, _ := testDeps()
	err := runApi([]string{"api/v3/test"}, deps)
	if err == nil || !strings.Contains(err.Error(), "endpoint must start with /") {
		t.Errorf("expected 'endpoint must start with /', got: %v", err)
	}
}

func TestRunApi_FullURLForbidden(t *testing.T) {
	deps, _, _ := testDeps()
	err := runApi([]string{"https://example.com/api/v3/test"}, deps)
	if err == nil || !strings.Contains(err.Error(), "endpoint must be a path, not a full URL") {
		t.Errorf("expected full URL error, got: %v", err)
	}
}

func TestRunApi_UnexpectedArgs(t *testing.T) {
	deps, _, _ := testDeps()
	err := runApi([]string{"/api/v3/test", "extra"}, deps)
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Errorf("expected unexpected arguments error, got: %v", err)
	}
}

func TestRunApi_SecretTokenMutualExclusion(t *testing.T) {
	deps, _, _ := testDeps()
	err := runApi([]string{"--secret", "s", "--token", "t", "/api/v3/test"}, deps)
	if err == nil || !strings.Contains(err.Error(), "--secret and --token are mutually exclusive") {
		t.Errorf("expected mutual exclusion error, got: %v", err)
	}
}

// --- reorderArgs with repeatable flags ---

func TestReorderArgs_RepeatableFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var fields stringSlice
	var method string
	fs.Var(&fields, "f", "")
	fs.StringVar(&method, "X", "", "")

	result := reorderArgs(fs, []string{"-f", "a=1", "-f", "b=2", "/api/endpoint"})
	// Endpoint should be at the end
	if len(result) != 5 {
		t.Fatalf("expected 5 args, got %d: %v", len(result), result)
	}
	if result[4] != "/api/endpoint" {
		t.Errorf("expected endpoint last, got: %v", result)
	}
}

// --- outputResponse ---

func TestOutputResponse_SilentWithInclude(t *testing.T) {
	deps, stdout, _ := testDeps()
	resp := &http.Response{
		StatusCode: 200,
		Proto:      "HTTP/1.1",
		Status:     "200 OK",
		Header:     http.Header{"X-Test": {"val"}},
		Body:       io.NopCloser(strings.NewReader("body")),
	}

	err := outputResponse(deps, resp, true, true, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "200 OK") {
		t.Errorf("expected status in output: %s", out)
	}
	// Body should be suppressed
	if strings.Contains(out, "body") {
		t.Errorf("expected body suppressed with --silent: %s", out)
	}
}

// --- ExitError ---

func TestExitError(t *testing.T) {
	err := &ExitError{Code: 1}
	if err.Error() != "exit status 1" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
	var e *ExitError
	if !errors.As(err, &e) {
		t.Error("expected errors.As to work with ExitError")
	}
}

// --- jq filtering ---

func TestParseJQ_Valid(t *testing.T) {
	if _, err := parseJQ(".foo"); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
	if _, err := parseJQ(".result[].name"); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
	if _, err := parseJQ(`.[] | select(.status == "ok")`); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
}

func TestParseJQ_Invalid(t *testing.T) {
	if _, err := parseJQ(".[invalid"); err == nil {
		t.Error("expected error for invalid expression")
	}
	if _, err := parseJQ(""); err == nil {
		t.Error("expected error for empty expression")
	}
}

func mustParseJQ(t *testing.T, expr string) *gojq.Query {
	t.Helper()
	q, err := parseJQ(expr)
	if err != nil {
		t.Fatalf("failed to parse jq expression %q: %v", expr, err)
	}
	return q
}

func TestApplyJQ_SimpleField(t *testing.T) {
	var stdout, stderr strings.Builder
	data := []byte(`{"name":"Alice","age":30}`)
	err := applyJQ(&stdout, &stderr, data, mustParseJQ(t, ".name"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "Alice" {
		t.Errorf("expected Alice, got %q", stdout.String())
	}
}

func TestApplyJQ_ArrayIteration(t *testing.T) {
	var stdout, stderr strings.Builder
	data := []byte(`{"result":[{"name":"a"},{"name":"b"}]}`)
	err := applyJQ(&stdout, &stderr, data, mustParseJQ(t, ".result[].name"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 || lines[0] != "a" || lines[1] != "b" {
		t.Errorf("expected [a, b], got %v", lines)
	}
}

func TestApplyJQ_NumericResult(t *testing.T) {
	var stdout, stderr strings.Builder
	data := []byte(`{"count":42}`)
	err := applyJQ(&stdout, &stderr, data, mustParseJQ(t, ".count"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "42" {
		t.Errorf("expected 42, got %q", stdout.String())
	}
}

func TestApplyJQ_NullResult(t *testing.T) {
	var stdout, stderr strings.Builder
	data := []byte(`{"a":1}`)
	err := applyJQ(&stdout, &stderr, data, mustParseJQ(t, ".missing"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "null" {
		t.Errorf("expected null, got %q", stdout.String())
	}
}

func TestApplyJQ_NonJSONInput(t *testing.T) {
	var stdout, stderr strings.Builder
	data := []byte("this is not JSON")
	err := applyJQ(&stdout, &stderr, data, mustParseJQ(t, ".foo"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout.String() != "this is not JSON" {
		t.Errorf("expected raw data in stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "not valid JSON") {
		t.Errorf("expected warning in stderr, got %q", stderr.String())
	}
}

func TestRunApi_JQFilter(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"result":[{"name":"chat1"},{"name":"chat2"}]}`)
	})

	cfgPath := apiTestConfig(t, ts.URL)
	deps, stdout, _ := testDeps()

	err := runApi([]string{
		"--config", cfgPath,
		"-q", ".result[].name",
		"/api/v3/chats/list",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 || lines[0] != "chat1" || lines[1] != "chat2" {
		t.Errorf("expected [chat1, chat2], got %v (raw: %q)", lines, stdout.String())
	}
}

func TestRunApi_JQFilterNonJSON(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "plain text response")
	})

	cfgPath := apiTestConfig(t, ts.URL)
	deps, stdout, stderr := testDeps()

	err := runApi([]string{
		"--config", cfgPath,
		"-q", ".foo",
		"/api/v3/test",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "plain text response") {
		t.Errorf("expected raw body in stdout: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "not valid JSON") {
		t.Errorf("expected warning in stderr: %s", stderr.String())
	}
}

func TestRunApi_InvalidJQExpression(t *testing.T) {
	deps, _, _ := testDeps()
	err := runApi([]string{"-q", ".[invalid", "/api/v3/test"}, deps)
	if err == nil || !strings.Contains(err.Error(), "invalid jq expression") {
		t.Errorf("expected invalid jq expression error, got: %v", err)
	}
}

func TestRunApi_JQOnNonSuccessResponse(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":"bad request","code":400}`)
	})

	cfgPath := apiTestConfig(t, ts.URL)
	deps, stdout, _ := testDeps()

	err := runApi([]string{
		"--config", cfgPath,
		"-q", ".error",
		"/api/v3/test",
	}, deps)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected ExitError, got: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "bad request" {
		t.Errorf("expected 'bad request', got %q", stdout.String())
	}
}

// --- stringSlice ---

func TestStringSlice(t *testing.T) {
	var s stringSlice
	s.Set("a")
	s.Set("b")
	if len(s) != 2 || s[0] != "a" || s[1] != "b" {
		t.Errorf("expected [a, b], got %v", s)
	}
	if s.String() != "a, b" {
		t.Errorf("expected 'a, b', got %q", s.String())
	}
}

// --- Multipart mode ---

func TestBuildAPIBody_MultipartBasic(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "photo.jpg")
	fileContent := []byte{0x89, 0x50, 0x4E, 0x47} // fake image data
	os.WriteFile(filePath, fileContent, 0644)

	body, err := buildAPIBody(apiBodyParams{
		inputFile: "@" + filePath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.method != "POST" {
		t.Errorf("expected POST, got %s", body.method)
	}
	if !strings.HasPrefix(body.contentType, "multipart/form-data") {
		t.Errorf("expected multipart/form-data, got %s", body.contentType)
	}
	if body.data == nil {
		t.Fatal("expected non-nil data")
	}
}

func TestBuildAPIBody_MultipartWithFields(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "photo.jpg")
	os.WriteFile(filePath, []byte("image-data"), 0644)

	body, err := buildAPIBody(apiBodyParams{
		inputFile: "@" + filePath,
		fields:    []string{"group_chat_id=test-uuid", "file_name=photo.jpg"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(body.contentType, "multipart/form-data") {
		t.Errorf("expected multipart/form-data, got %s", body.contentType)
	}
}

func TestBuildAPIBody_MultipartCustomPartName(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "doc.pdf")
	os.WriteFile(filePath, []byte("pdf-data"), 0644)

	body, err := buildAPIBody(apiBodyParams{
		inputFile: "@" + filePath,
		partName:  "file",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(body.contentType, "multipart/form-data") {
		t.Errorf("expected multipart/form-data, got %s", body.contentType)
	}
	// Verify the part name is used by checking the body contains "file"
	if !strings.Contains(string(body.data), `name="file"`) {
		t.Errorf("expected part name 'file' in body, got: %s", string(body.data))
	}
}

func TestBuildAPIBody_MultipartDefaultPartName(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "photo.jpg")
	os.WriteFile(filePath, []byte("data"), 0644)

	body, err := buildAPIBody(apiBodyParams{
		inputFile: "@" + filePath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(body.data), `name="content"`) {
		t.Errorf("expected default part name 'content' in body, got: %s", string(body.data))
	}
}

func TestBuildAPIBody_MultipartTypedFieldsError(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "photo.jpg")
	os.WriteFile(filePath, []byte("data"), 0644)

	_, err := buildAPIBody(apiBodyParams{
		inputFile:   "@" + filePath,
		typedFields: []string{"key=val"},
	})
	if err == nil || !strings.Contains(err.Error(), "-F is not supported in multipart mode") {
		t.Errorf("expected -F multipart error, got: %v", err)
	}
}

func TestBuildAPIBody_MultipartFileNotFound(t *testing.T) {
	_, err := buildAPIBody(apiBodyParams{
		inputFile: "@/nonexistent/file.jpg",
	})
	if err == nil || !strings.Contains(err.Error(), "reading input file") {
		t.Errorf("expected file read error, got: %v", err)
	}
}

func TestBuildAPIBody_MultipartExplicitMethod(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "photo.jpg")
	os.WriteFile(filePath, []byte("data"), 0644)

	body, err := buildAPIBody(apiBodyParams{
		method:    "PUT",
		inputFile: "@" + filePath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body.method != "PUT" {
		t.Errorf("expected PUT, got %s", body.method)
	}
}

func TestRunApi_MultipartUpload(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "multipart/form-data") {
			t.Errorf("expected multipart/form-data, got %s", ct)
		}

		// Parse multipart form
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("failed to parse multipart: %v", err)
		}

		// Check text fields
		if r.FormValue("group_chat_id") != "test-uuid" {
			t.Errorf("expected group_chat_id=test-uuid, got %s", r.FormValue("group_chat_id"))
		}

		// Check file part
		file, header, err := r.FormFile("content")
		if err != nil {
			t.Fatalf("expected file part 'content': %v", err)
		}
		defer file.Close()
		if header.Filename != "photo.jpg" {
			t.Errorf("expected filename photo.jpg, got %s", header.Filename)
		}
		data, _ := io.ReadAll(file)
		if string(data) != "fake-image-data" {
			t.Errorf("expected 'fake-image-data', got %q", string(data))
		}

		fmt.Fprint(w, `{"result":"uploaded"}`)
	})

	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "photo.jpg")
	os.WriteFile(filePath, []byte("fake-image-data"), 0644)

	cfgPath := apiTestConfig(t, ts.URL)
	deps, stdout, _ := testDeps()

	err := runApi([]string{
		"--config", cfgPath,
		"-X", "POST",
		"--input", "@" + filePath,
		"-f", "group_chat_id=test-uuid",
		"/api/v3/botx/files/upload",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "uploaded") {
		t.Errorf("unexpected output: %s", stdout.String())
	}
}

func TestRunApi_MultipartCustomPartName(t *testing.T) {
	ts := apiTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("failed to parse multipart: %v", err)
		}

		// Check file part with custom name
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("expected file part 'file': %v", err)
		}
		file.Close()

		fmt.Fprint(w, `{"result":"ok"}`)
	})

	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "doc.pdf")
	os.WriteFile(filePath, []byte("pdf-content"), 0644)

	cfgPath := apiTestConfig(t, ts.URL)
	deps, _, _ := testDeps()

	err := runApi([]string{
		"--config", cfgPath,
		"-X", "POST",
		"--input", "@" + filePath,
		"--part-name", "file",
		"/api/v3/botx/files/upload",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
