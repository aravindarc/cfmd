// Package confluence provides a minimal HTTP client for the subset of the
// Confluence Cloud REST API v1 that cfmd uses. Authentication is HTTP Basic
// with username + API token.
package confluence

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aravindarc/cfmd/internal/config"
)

// Client talks to a Confluence Cloud site.
type Client struct {
	baseURL    string
	username   string
	token      string
	httpClient *http.Client

	// Verbose, if true, logs request/response summaries to stderr (never the
	// auth header or token).
	Verbose bool
}

// New constructs a Client from config. Call cfg.RequireAuth() first if you
// want a friendly error when credentials are missing.
func New(cfg *config.Config) *Client {
	httpCl := &http.Client{
		Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
	}
	if cfg.AllowInsecureTLS {
		httpCl.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	return &Client{
		baseURL:    cfg.BaseURL,
		username:   cfg.Username,
		token:      cfg.Token,
		httpClient: httpCl,
	}
}

func (c *Client) authHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(c.username+":"+c.token))
}

// do issues a JSON request, handling auth, retries, and error classification.
// If body is non-nil, it is JSON-encoded and sent. On 2xx, the response is
// decoded into out (if non-nil). On non-2xx, a typed error is returned.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	fullURL := c.baseURL + path

	// Retry policy: 429 and 5xx errors retry up to 3 times with a modest
	// backoff. 4xx (except 429) return immediately — they're not transient.
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			// Backoff: 1s, 2s, 4s. Respect Retry-After if present.
			delay := time.Duration(1<<uint(attempt-1)) * time.Second
			time.Sleep(delay)
		}
		var freshBody io.Reader = reqBody
		if reqBody != nil && attempt > 0 {
			// http.NewRequest consumes the reader; re-marshal on retry.
			b, _ := json.Marshal(body)
			freshBody = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, fullURL, freshBody)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", c.authHeader())
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = &APIError{Status: 0, URL: fullURL, Body: err.Error(), Class: ErrAPI}
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if out != nil && len(body) > 0 {
				if err := json.Unmarshal(body, out); err != nil {
					return fmt.Errorf("decode response from %s: %w\nbody: %s", fullURL, err, truncate(string(body), 400))
				}
			}
			return nil
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return &APIError{Status: resp.StatusCode, URL: fullURL, Body: string(body), Class: ErrAuth}
		case resp.StatusCode == 404:
			return &APIError{Status: resp.StatusCode, URL: fullURL, Body: string(body), Class: ErrNotFound}
		case resp.StatusCode == 409:
			return &APIError{Status: resp.StatusCode, URL: fullURL, Body: string(body), Class: ErrConflict}
		case resp.StatusCode == 429 || resp.StatusCode >= 500:
			// Retryable; fall through to next attempt.
			lastErr = &APIError{Status: resp.StatusCode, URL: fullURL, Body: string(body), Class: ErrAPI}
			continue
		default:
			return &APIError{Status: resp.StatusCode, URL: fullURL, Body: string(body), Class: ErrAPI}
		}
	}
	if lastErr == nil {
		lastErr = &APIError{Status: 0, URL: fullURL, Body: "retries exhausted", Class: ErrAPI}
	}
	return lastErr
}

// GetCurrentUser verifies credentials by hitting /rest/api/user/current.
func (c *Client) GetCurrentUser(ctx context.Context) (*CurrentUser, error) {
	var u CurrentUser
	if err := c.do(ctx, http.MethodGet, "/rest/api/user/current", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetPage fetches a page by ID with storage body, version, space, and
// ancestors expanded.
func (c *Client) GetPage(ctx context.Context, id string) (*Page, error) {
	q := url.Values{}
	q.Set("expand", "body.storage,version,space,ancestors")
	path := "/rest/api/content/" + url.PathEscape(id) + "?" + q.Encode()
	var p Page
	if err := c.do(ctx, http.MethodGet, path, nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPageVersion fetches just the version number for a page (cheap; used for
// pre-push version check).
func (c *Client) GetPageVersion(ctx context.Context, id string) (int, error) {
	q := url.Values{}
	q.Set("expand", "version")
	path := "/rest/api/content/" + url.PathEscape(id) + "?" + q.Encode()
	var p Page
	if err := c.do(ctx, http.MethodGet, path, nil, &p); err != nil {
		return 0, err
	}
	return p.Version.Number, nil
}

// CreatePage creates a new page.
func (c *Client) CreatePage(ctx context.Context, req *PageCreate) (*Page, error) {
	var p Page
	if err := c.do(ctx, http.MethodPost, "/rest/api/content", req, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdatePage updates an existing page. The new version.number must be one
// greater than the current remote version.
func (c *Client) UpdatePage(ctx context.Context, id string, req *PageUpdate) (*Page, error) {
	path := "/rest/api/content/" + url.PathEscape(id)
	var p Page
	if err := c.do(ctx, http.MethodPut, path, req, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// UploadAttachment attaches a file to a page. If an attachment with the same
// filename exists, this will create a new version of it.
func (c *Client) UploadAttachment(ctx context.Context, pageID, filename string, data io.Reader) error {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, data); err != nil {
		return err
	}
	if err := w.WriteField("minorEdit", "true"); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	path := "/rest/api/content/" + url.PathEscape(pageID) + "/child/attachment"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.authHeader())
	req.Header.Set("X-Atlassian-Token", "no-check")
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, URL: c.baseURL + path, Body: string(respBody), Class: ErrAPI}
	}
	return nil
}

// BuildPageURL returns the human-facing Confluence URL for a page, given the
// `_links` object in the response. If links is insufficient, returns empty.
func (c *Client) BuildPageURL(p *Page) string {
	base := c.baseURL
	if p.Links.Base != "" {
		base = strings.TrimRight(p.Links.Base, "/")
	}
	if p.Links.WebUI != "" {
		return base + p.Links.WebUI
	}
	if p.ID != "" {
		return base + "/pages/viewpage.action?pageId=" + p.ID
	}
	return ""
}

// ParsePageIDFromURL extracts the numeric page ID from a Confluence URL. It
// supports the common forms:
//   - .../wiki/spaces/KEY/pages/12345/Title
//   - .../wiki/pages/viewpage.action?pageId=12345
//   - bare numeric string (returns it unchanged)
func ParsePageIDFromURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	// Bare ID?
	if _, err := strconv.Atoi(raw); err == nil {
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	// Query parameter form.
	if id := u.Query().Get("pageId"); id != "" {
		return id, nil
	}
	// Path form: .../pages/<id>/...
	segs := strings.Split(u.Path, "/")
	for i := 0; i < len(segs)-1; i++ {
		if segs[i] == "pages" {
			if _, err := strconv.Atoi(segs[i+1]); err == nil {
				return segs[i+1], nil
			}
		}
	}
	return "", fmt.Errorf("could not extract page id from URL %q", raw)
}

// SameHostAsBase checks that the given URL's host matches the configured
// base URL's host. Returns nil if matching, an error otherwise. Used to
// reject `cfmd pull <url>` from a Confluence instance other than the one the
// user configured.
func (c *Client) SameHostAsBase(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Host == "" {
		// Relative / bare ID; no host to compare.
		return nil
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return err
	}
	if u.Host != base.Host {
		return fmt.Errorf("url host %q does not match configured base host %q", u.Host, base.Host)
	}
	return nil
}
