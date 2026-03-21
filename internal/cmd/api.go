package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/itchyny/gojq"
	"github.com/lavr/express-botx/internal/botapi"
	"github.com/lavr/express-botx/internal/config"
	vlog "github.com/lavr/express-botx/internal/log"
	"github.com/lavr/express-botx/internal/token"
)

// ExitError allows commands to exit with a specific code without printing
// "error: ..." to stderr. Used by api command for non-2xx HTTP responses.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

// maxRequestBodySize is the maximum allowed request body size (50 MB).
const maxRequestBodySize = 50 * 1024 * 1024

// stringSlice implements flag.Value for repeatable string flags.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ", ") }
func (s *stringSlice) Set(val string) error {
	*s = append(*s, val)
	return nil
}

// apiBody holds the constructed request body.
type apiBody struct {
	data        []byte // serialized body (JSON, raw) or nil for GET
	contentType string // Content-Type or "" (raw mode — user sets via -H)
	method      string // final HTTP method (after auto-selection)
}

type apiBodyParams struct {
	method      string    // HTTP method (or empty for auto-selection)
	fields      []string  // -f key=value
	typedFields []string  // -F key=value (auto type coercion)
	inputFile   string    // --input (path, "-" for stdin, "@file" for multipart)
	partName    string    // --part-name (default "content")
	stdin       io.Reader // deps.Stdin
}

func buildAPIBody(p apiBodyParams) (*apiBody, error) {
	hasFields := len(p.fields) > 0 || len(p.typedFields) > 0
	hasInput := p.inputFile != ""
	isMultipart := hasInput && strings.HasPrefix(p.inputFile, "@")

	// Validate mutual exclusions
	if hasInput && !isMultipart && hasFields {
		return nil, fmt.Errorf("--input and -f/-F are mutually exclusive (use --input @file for multipart)")
	}
	if isMultipart && len(p.typedFields) > 0 {
		return nil, fmt.Errorf("-F is not supported in multipart mode, use -f for text parts")
	}

	// Determine method
	method := p.method
	if method == "" {
		if hasFields || hasInput {
			method = "POST"
		} else {
			method = "GET"
		}
	}

	// No body
	if !hasFields && !hasInput {
		return &apiBody{method: method}, nil
	}

	// Raw mode: --input without @ prefix
	if hasInput && !isMultipart {
		var data []byte
		var err error
		if p.inputFile == "-" {
			data, err = io.ReadAll(io.LimitReader(p.stdin, maxRequestBodySize+1))
		} else {
			if fi, statErr := os.Stat(p.inputFile); statErr == nil && fi.Size() > maxRequestBodySize {
				return nil, fmt.Errorf("request body too large (max 50MB)")
			}
			data, err = os.ReadFile(p.inputFile)
		}
		if err != nil {
			return nil, fmt.Errorf("reading input: %w", err)
		}
		if len(data) > maxRequestBodySize {
			return nil, fmt.Errorf("request body too large (max 50MB)")
		}
		return &apiBody{data: data, method: method}, nil
	}

	// Multipart mode: --input @file
	if isMultipart {
		filePath := p.inputFile[1:] // strip @ prefix
		if filePath == "" {
			return nil, fmt.Errorf("missing file path after @")
		}
		if fi, statErr := os.Stat(filePath); statErr == nil && fi.Size() > maxRequestBodySize {
			return nil, fmt.Errorf("request body too large (max 50MB)")
		}
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("reading input file: %w", err)
		}
		if len(fileData) > maxRequestBodySize {
			return nil, fmt.Errorf("request body too large (max 50MB)")
		}

		partName := p.partName
		if partName == "" {
			partName = "content"
		}

		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)

		// Add binary file part
		part, err := w.CreateFormFile(partName, filepath.Base(filePath))
		if err != nil {
			return nil, fmt.Errorf("creating multipart file part: %w", err)
		}
		if _, err := part.Write(fileData); err != nil {
			return nil, fmt.Errorf("writing multipart file part: %w", err)
		}

		// Add text parts from -f fields
		for _, f := range p.fields {
			key, val, ok := strings.Cut(f, "=")
			if !ok {
				return nil, fmt.Errorf("invalid field format %q (expected key=value)", f)
			}
			if err := w.WriteField(key, val); err != nil {
				return nil, fmt.Errorf("writing multipart field %q: %w", key, err)
			}
		}

		if err := w.Close(); err != nil {
			return nil, fmt.Errorf("closing multipart writer: %w", err)
		}

		if buf.Len() > maxRequestBodySize {
			return nil, fmt.Errorf("request body too large (max 50MB)")
		}

		return &apiBody{data: buf.Bytes(), contentType: w.FormDataContentType(), method: method}, nil
	}

	// JSON mode: -f/-F fields without multipart
	if hasFields {
		obj := make(map[string]any)
		for _, f := range p.fields {
			key, val, ok := strings.Cut(f, "=")
			if !ok {
				return nil, fmt.Errorf("invalid field format %q (expected key=value)", f)
			}
			obj[key] = val
		}
		for _, f := range p.typedFields {
			key, val, ok := strings.Cut(f, "=")
			if !ok {
				return nil, fmt.Errorf("invalid field format %q (expected key=value)", f)
			}
			typed, err := parseTypedValue(val)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", key, err)
			}
			obj[key] = typed
		}
		data, err := json.Marshal(obj)
		if err != nil {
			return nil, fmt.Errorf("marshaling JSON body: %w", err)
		}
		return &apiBody{data: data, contentType: "application/json", method: method}, nil
	}

	return &apiBody{method: method}, nil
}

// parseTypedValue converts a string value to a typed value for -F fields:
// "true"/"false" → bool, integer strings → number, @filename → file contents as string.
func parseTypedValue(val string) (any, error) {
	if val == "true" {
		return true, nil
	}
	if val == "false" {
		return false, nil
	}
	if strings.HasPrefix(val, "@") {
		path := val[1:]
		if path == "" {
			return nil, fmt.Errorf("missing file path after @")
		}
		if fi, statErr := os.Stat(path); statErr == nil && fi.Size() > maxRequestBodySize {
			return nil, fmt.Errorf("file %q too large (max 50MB)", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading file %q: %w", path, err)
		}
		if len(data) > maxRequestBodySize {
			return nil, fmt.Errorf("file %q too large (max 50MB)", path)
		}
		return string(data), nil
	}
	if n, err := strconv.ParseInt(val, 10, 64); err == nil {
		return n, nil
	}
	return val, nil
}

func hasAuthHeader(headers []string) bool {
	for _, h := range headers {
		key, _, ok := strings.Cut(h, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "authorization") {
			return true
		}
	}
	return false
}

func buildHTTPRequest(ctx context.Context, body *apiBody, endpoint, baseURL, tok string, headers []string) (*http.Request, error) {
	url := baseURL + endpoint

	var bodyReader io.Reader
	if body.data != nil {
		bodyReader = bytes.NewReader(body.data)
	}

	req, err := http.NewRequestWithContext(ctx, body.method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Set Authorization if not overridden by user
	if tok != "" && !hasAuthHeader(headers) {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	// Set Content-Type from body
	if body.contentType != "" {
		req.Header.Set("Content-Type", body.contentType)
	}

	// Apply custom headers (may override defaults)
	for _, h := range headers {
		key, val, ok := strings.Cut(h, ":")
		if !ok {
			return nil, fmt.Errorf("invalid header format %q (expected key:value)", h)
		}
		req.Header.Set(strings.TrimSpace(key), strings.TrimSpace(val))
	}

	return req, nil
}

func runApi(args []string, deps Deps) error {
	fs := flag.NewFlagSet("api", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)

	var flags config.Flags
	var method string
	var fields stringSlice
	var typedFields stringSlice
	var headers stringSlice
	var inputFile string
	var partName string
	var jqExpr string
	var include bool
	var reqTimeout time.Duration
	var silent bool

	globalFlags(fs, &flags)
	fs.StringVar(&method, "X", "", "HTTP method")
	fs.StringVar(&method, "method", "", "HTTP method")
	fs.Var(&fields, "f", "string field for JSON body (key=value, repeatable)")
	fs.Var(&fields, "field", "string field for JSON body (key=value, repeatable)")
	fs.Var(&typedFields, "F", "typed field: true/false→bool, int→number, @file→contents (key=value, repeatable)")
	fs.Var(&headers, "H", "custom HTTP header (key:value, repeatable)")
	fs.Var(&headers, "header", "custom HTTP header (key:value, repeatable)")
	fs.StringVar(&inputFile, "input", "", "file with request body (- for stdin, @file for multipart)")
	fs.StringVar(&partName, "part-name", "content", "multipart part name for binary file")
	fs.StringVar(&jqExpr, "q", "", "jq expression for filtering JSON response")
	fs.StringVar(&jqExpr, "jq", "", "jq expression for filtering JSON response")
	fs.BoolVar(&include, "i", false, "show HTTP status and response headers")
	fs.BoolVar(&include, "include", false, "show HTTP status and response headers")
	fs.DurationVar(&reqTimeout, "timeout", 0, "HTTP request timeout (overrides config)")
	fs.BoolVar(&silent, "silent", false, "suppress response body output")

	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, `Usage: express-botx api [options] <endpoint>

Make authenticated HTTP requests to eXpress BotX API.

Examples:
  express-botx api /api/v3/botx/chats/list
  express-botx api -X POST /api/v3/botx/chats/create -f name=test
  express-botx api /api/v3/botx/chats/list -q '.result[].name'

Options:
`)
		fs.PrintDefaults()
	}

	if hasHelpFlag(args) {
		fs.Usage()
		return nil
	}

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	applyVerboseFlag(flags)

	// Validate endpoint
	if fs.NArg() == 0 {
		return fmt.Errorf("endpoint required")
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args()[1:], " "))
	}
	endpoint := fs.Arg(0)
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return fmt.Errorf("endpoint must be a path, not a full URL")
	}
	if !strings.HasPrefix(endpoint, "/") {
		return fmt.Errorf("endpoint must start with /")
	}

	// Parse jq expression early (before auth/request)
	var jqQuery *gojq.Query
	if jqExpr != "" {
		var err error
		jqQuery, err = parseJQ(jqExpr)
		if err != nil {
			return fmt.Errorf("invalid jq expression: %w", err)
		}
	}

	// Validate secret/token mutual exclusion
	if flags.Secret != "" && flags.Token != "" {
		return fmt.Errorf("--secret and --token are mutually exclusive")
	}

	manualAuth := hasAuthHeader(headers)

	// Load config
	cfg, err := config.LoadForAPI(flags, manualAuth)
	if err != nil {
		return err
	}
	if err := cfg.ValidateFormat(); err != nil {
		return err
	}

	// Build request body (before auth — validate inputs first)
	body, err := buildAPIBody(apiBodyParams{
		method:      method,
		fields:      fields,
		typedFields: typedFields,
		inputFile:   inputFile,
		partName:    partName,
		stdin:       deps.Stdin,
	})
	if err != nil {
		return err
	}

	// Authenticate (skip if manual Authorization header)
	var authToken string
	var authCache token.Cache
	if !manualAuth {
		t, c, authErr := authenticate(cfg)
		if authErr != nil {
			return authErr
		}
		authToken = t
		authCache = c
	}

	// Determine timeout
	httpTimeout := cfg.HTTPTimeout()
	if reqTimeout > 0 {
		httpTimeout = reqTimeout
	}

	baseURL := botapi.ResolveBaseURL(cfg.Host)
	ctx := context.Background()

	// Build and execute request
	req, err := buildHTTPRequest(ctx, body, endpoint, baseURL, authToken, headers)
	if err != nil {
		return err
	}

	vlog.V1("api: %s %s", body.method, req.URL.String())

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	// Handle 401 retry
	if resp.StatusCode == http.StatusUnauthorized && !manualAuth {
		resp.Body.Close()

		if cfg.BotToken != "" {
			return fmt.Errorf("bot token rejected (401), re-configure token")
		}

		vlog.V1("api: 401, refreshing token")
		newTok, err := refreshToken(cfg, authCache)
		if err != nil {
			return fmt.Errorf("refreshing token: %w", err)
		}
		authToken = newTok

		req, err = buildHTTPRequest(ctx, body, endpoint, baseURL, authToken, headers)
		if err != nil {
			return err
		}

		resp, err = client.Do(req)
		if err != nil {
			return fmt.Errorf("request failed after token refresh: %w", err)
		}

		if resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			return fmt.Errorf("unauthorized after token refresh (401)")
		}
	}
	defer resp.Body.Close()

	return outputResponse(deps, resp, include, silent, jqQuery, cfg.Format)
}

func outputResponse(deps Deps, resp *http.Response, include, silent bool, jqQuery *gojq.Query, format string) error {
	if include {
		if _, err := fmt.Fprintf(deps.Stdout, "%s %s\n", resp.Proto, resp.Status); err != nil {
			return fmt.Errorf("writing response: %w", err)
		}
		for key, vals := range resp.Header {
			for _, v := range vals {
				if _, err := fmt.Fprintf(deps.Stdout, "%s: %s\n", key, v); err != nil {
					return fmt.Errorf("writing response: %w", err)
				}
			}
		}
		if _, err := fmt.Fprintln(deps.Stdout); err != nil {
			return fmt.Errorf("writing response: %w", err)
		}
	}

	isSuccess := resp.StatusCode >= 200 && resp.StatusCode < 300

	if !silent {
		if isSuccess {
			if jqQuery != nil {
				data, err := io.ReadAll(resp.Body)
				if err != nil {
					return fmt.Errorf("reading response: %w", err)
				}
				if err := applyJQ(deps.Stdout, deps.Stderr, data, jqQuery); err != nil {
					// If the error is a write failure (stdout broken), propagate
					// it directly instead of retrying with raw output.
					if strings.HasPrefix(err.Error(), "writing output:") {
						return fmt.Errorf("writing response: %w", err)
					}
					fmt.Fprintf(deps.Stderr, "warning: jq filter failed: %v\n", err)
					if _, wErr := deps.Stdout.Write(data); wErr != nil {
						return fmt.Errorf("writing response: %w", wErr)
					}
				}
			} else if format == "json" && isJSONContentType(resp.Header.Get("Content-Type")) {
				data, err := io.ReadAll(resp.Body)
				if err != nil {
					return fmt.Errorf("reading response: %w", err)
				}
				if err := prettyPrintJSON(deps.Stdout, data); err != nil {
					return fmt.Errorf("writing response: %w", err)
				}
			} else {
				if _, err := io.Copy(deps.Stdout, resp.Body); err != nil {
					return fmt.Errorf("writing response: %w", err)
				}
			}
		} else {
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("reading response: %w", err)
			}
			if jqQuery != nil {
				if err := applyJQ(deps.Stdout, deps.Stderr, data, jqQuery); err != nil {
					// If the error is a write failure (stdout broken), propagate
					// it directly instead of retrying with raw output.
					if strings.HasPrefix(err.Error(), "writing output:") {
						return fmt.Errorf("writing response: %w", err)
					}
					fmt.Fprintf(deps.Stderr, "warning: jq filter failed: %v\n", err)
					if _, wErr := deps.Stdout.Write(data); wErr != nil {
						return fmt.Errorf("writing response: %w", wErr)
					}
				}
			} else if format == "json" && isJSONContentType(resp.Header.Get("Content-Type")) {
				if err := prettyPrintJSON(deps.Stdout, data); err != nil {
					return fmt.Errorf("writing response: %w", err)
				}
			} else {
				if _, err := deps.Stdout.Write(data); err != nil {
					return fmt.Errorf("writing response: %w", err)
				}
			}
		}
	}

	if !isSuccess {
		return &ExitError{Code: 1}
	}
	return nil
}

func isJSONContentType(ct string) bool {
	return strings.Contains(ct, "application/json")
}

func prettyPrintJSON(w io.Writer, data []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		_, wErr := w.Write(data)
		return wErr
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// parseJQ parses a jq expression and returns the compiled query.
func parseJQ(expr string) (*gojq.Query, error) {
	if expr == "" {
		return nil, fmt.Errorf("empty expression")
	}
	return gojq.Parse(expr)
}

// applyJQ applies a parsed jq query to JSON data and writes results to stdout.
// If data is not valid JSON, writes raw data to stdout and a warning to stderr.
func applyJQ(stdout io.Writer, stderr io.Writer, data []byte, query *gojq.Query) error {
	var input any
	if err := json.Unmarshal(data, &input); err != nil {
		if _, wErr := stdout.Write(data); wErr != nil {
			return fmt.Errorf("writing output: %w", wErr)
		}
		fmt.Fprintf(stderr, "warning: response is not valid JSON, showing raw output\n")
		return nil
	}

	iter := query.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := v.(error); isErr {
			return fmt.Errorf("jq error: %w", err)
		}
		switch val := v.(type) {
		case string:
			if _, err := fmt.Fprintln(stdout, val); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}
		default:
			out, err := json.Marshal(val)
			if err != nil {
				return fmt.Errorf("marshaling jq result: %w", err)
			}
			if _, err := fmt.Fprintln(stdout, string(out)); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}
		}
	}
	return nil
}
