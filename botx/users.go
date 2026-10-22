package botx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
)

// User is an eXpress user as returned by the users/by_* calls.
type User struct {
	HUID       string   `json:"user_huid"`
	Name       string   `json:"name"`
	Emails     []string `json:"emails"`
	ADLogin    string   `json:"ad_login,omitempty"`
	ADDomain   string   `json:"ad_domain,omitempty"`
	Company    string   `json:"company,omitempty"`
	Title      string   `json:"company_position,omitempty"`
	Department string   `json:"department,omitempty"`
	Active     bool     `json:"active"`
	UserKind   string   `json:"user_kind,omitempty"`
}

// GetUserByHUID looks a user up by HUID. An unknown user fails with ErrNotFound,
// a result for another HUID with ErrInvalidResponse.
func (c *Client) GetUserByHUID(ctx context.Context, huid string) (*User, error) {
	return c.getUser(ctx, "/api/v3/botx/users/by_huid?user_huid="+url.QueryEscape(huid), huid)
}

// GetUserByEmail looks a user up by email. An unknown user fails with ErrNotFound.
func (c *Client) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return c.getUser(ctx, "/api/v3/botx/users/by_email?email="+url.QueryEscape(email), "")
}

// GetUserByADLogin looks a user up by AD login and domain. An unknown user fails with ErrNotFound.
func (c *Client) GetUserByADLogin(ctx context.Context, login, domain string) (*User, error) {
	return c.getUser(ctx, "/api/v3/botx/users/by_login?ad_login="+url.QueryEscape(login)+"&ad_domain="+url.QueryEscape(domain), "")
}

// getUser fetches a user and, when wantHUID is set, checks the result is that user.
func (c *Client) getUser(ctx context.Context, path, wantHUID string) (*User, error) {
	vlog.V2("user: GET %s", c.baseURL+path)

	start := time.Now()
	resp, err := c.do(ctx, http.MethodGet, path, "", nil)
	if err != nil {
		return nil, fmt.Errorf("getting user: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	elapsed := time.Since(start)

	respBody, readErr := readResponse("get user", resp)
	if resp.StatusCode == http.StatusUnauthorized {
		vlog.V1("user: <- 401 Unauthorized (%dms)", elapsed.Milliseconds())
		return nil, unauthorized("get user", respBody)
	}
	if resp.StatusCode != http.StatusOK {
		vlog.V1("user: <- %d (%dms)", resp.StatusCode, elapsed.Milliseconds())
		return nil, newAPIError("get user", resp.StatusCode, respBody, "get user failed: HTTP %d: %s", resp.StatusCode, truncateBody(respBody))
	}
	if readErr != nil {
		return nil, readErr
	}

	var result struct {
		Status string `json:"status"`
		Result *User  `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, newAPIError("get user", resp.StatusCode, respBody, "decoding response: %v", err)
	}
	if result.Status != "" && result.Status != "ok" {
		return nil, newAPIError("get user", resp.StatusCode, respBody, "get user failed: status %q", result.Status)
	}
	if result.Result == nil || result.Result.HUID == "" {
		return nil, newAPIError("get user", resp.StatusCode, respBody, "get user failed: response has no user_huid")
	}
	if wantHUID != "" && !strings.EqualFold(result.Result.HUID, wantHUID) {
		return nil, newAPIError("get user", resp.StatusCode, respBody, "get user failed: response is for user %q, requested %q", result.Result.HUID, wantHUID)
	}

	vlog.V1("user: <- %d %s (%dms)", resp.StatusCode, http.StatusText(resp.StatusCode), elapsed.Milliseconds())
	return result.Result, nil
}
