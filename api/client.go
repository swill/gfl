// Package api implements the Confluence REST API v1 client. All requests
// use Basic Auth and JSON payloads. The client returns tree.CfNode values
// so that the orchestrator can build a CfTree directly from API results.
package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client is a Confluence REST API v1 client.
type Client struct {
	baseURL    string // e.g. "https://org.atlassian.net/wiki"
	authHeader string // "Basic <base64(user:token)>"
	http       *http.Client
}

// NewClient creates a client from the base URL, user, and API token.
// The baseURL should not have a trailing slash.
func NewClient(baseURL, user, apiToken string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	cred := base64.StdEncoding.EncodeToString([]byte(user + ":" + apiToken))
	return &Client{
		baseURL:    baseURL,
		authHeader: "Basic " + cred,
		http:       &http.Client{},
	}
}

// APIError represents a non-2xx HTTP response from Confluence.
type APIError struct {
	StatusCode int
	Status     string
	Body       string
	URL        string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("confluence API %s: %s", e.Status, e.URL)
}

// IsConflict returns true if the error is a 409 version conflict.
func IsConflict(err error) bool {
	ae, ok := err.(*APIError)
	return ok && ae.StatusCode == http.StatusConflict
}

// IsNotFound returns true if the error is a 404.
func IsNotFound(err error) bool {
	ae, ok := err.(*APIError)
	return ok && ae.StatusCode == http.StatusNotFound
}

// IsAttachmentUnchanged returns true when an UploadAttachment call failed
// because the bytes posted are byte-identical to the latest version of the
// attachment already on the page.
//
// Confluence Cloud's POST /content/{id}/child/attachment returns HTTP 400
// with a message containing "same file name as an existing attachment" in
// this case. (Different bytes for the same filename succeed and create a
// new version; only an exact-byte re-upload is rejected.)
//
// Callers — chiefly the push path — treat this as a silent success: the
// attachment is already up to date, so the page's <ri:attachment>
// reference resolves correctly, and there is nothing to retry on a
// subsequent push.
func IsAttachmentUnchanged(err error) bool {
	ae, ok := err.(*APIError)
	if !ok || ae.StatusCode != http.StatusBadRequest {
		return false
	}
	// Match a few phrasings Confluence has used for this case so the
	// detector is resilient to minor message tweaks across versions.
	body := strings.ToLower(ae.Body)
	for _, marker := range []string{
		"same file name as an existing attachment",
		"file with the same name already exists",
		"attachment data has not changed",
	} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

// do executes an HTTP request with auth headers set and returns the response.
// The caller is responsible for closing the response body.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", c.authHeader)
	if req.Header.Get("Content-Type") == "" && req.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

// doJSON executes a JSON request and decodes the response into dest.
func (c *Client) doJSON(method, url string, body io.Reader, dest any) error {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return err
	}
	if dest != nil {
		return json.NewDecoder(resp.Body).Decode(dest)
	}
	return nil
}

// checkResponse returns an *APIError if the status code is not 2xx.
func checkResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return &APIError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Body:       string(body),
		URL:        resp.Request.URL.String(),
	}
}
