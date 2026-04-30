package api

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClient_AuthHeader(t *testing.T) {
	c := NewClient("https://org.atlassian.net/wiki", "user@org.com", "tok123")
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user@org.com:tok123"))
	if c.authHeader != want {
		t.Errorf("authHeader = %q, want %q", c.authHeader, want)
	}
}

func TestNewClient_TrailingSlash(t *testing.T) {
	c := NewClient("https://org.atlassian.net/wiki/", "u", "t")
	if c.baseURL != "https://org.atlassian.net/wiki" {
		t.Errorf("baseURL = %q, want trailing slash stripped", c.baseURL)
	}
}

func TestAPIError_Error(t *testing.T) {
	e := &APIError{StatusCode: 409, Status: "409 Conflict", Body: "version", URL: "https://x/rest/api/content/1"}
	msg := e.Error()
	if msg == "" {
		t.Fatal("empty error message")
	}
}

func TestIsConflict(t *testing.T) {
	if IsConflict(nil) {
		t.Error("nil should not be conflict")
	}
	if IsConflict(&APIError{StatusCode: 404}) {
		t.Error("404 should not be conflict")
	}
	if !IsConflict(&APIError{StatusCode: 409}) {
		t.Error("409 should be conflict")
	}
}

func TestIsNotFound(t *testing.T) {
	if IsNotFound(nil) {
		t.Error("nil should not be not-found")
	}
	if IsNotFound(&APIError{StatusCode: 500}) {
		t.Error("500 should not be not-found")
	}
	if !IsNotFound(&APIError{StatusCode: 404}) {
		t.Error("404 should be not-found")
	}
}

func TestIsAttachmentUnchanged(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"non-API error", errString("network down"), false},
		{"404", &APIError{StatusCode: 404, Body: "not found"}, false},
		{"500", &APIError{StatusCode: 500, Body: "boom"}, false},
		{"400 unrelated", &APIError{StatusCode: 400, Body: "missing field"}, false},
		{
			"400 same-file-name (Cloud canonical)",
			&APIError{StatusCode: 400, Body: `{"message":"Cannot add a new attachment with same file name as an existing attachment: foo.png"}`},
			true,
		},
		{
			"400 file-with-same-name",
			&APIError{StatusCode: 400, Body: `{"message":"A file with the same name already exists"}`},
			true,
		},
		{
			"400 case-insensitive",
			&APIError{StatusCode: 400, Body: `{"message":"SAME FILE NAME AS AN EXISTING ATTACHMENT"}`},
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsAttachmentUnchanged(c.err); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestDo_SetsAuthHeader(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "user", "pass")
	c.SetHTTPClient(ts.Client())

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/test", nil)
	resp, err := c.do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestDo_SetsContentType(t *testing.T) {
	var gotCT string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/test", http.NoBody)
	req.Body = http.NoBody
	// With a non-nil body, Content-Type should be set.
	resp, err := c.do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()

	// http.NoBody is treated as non-nil by the header check.
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
}

func TestCheckResponse_Success(t *testing.T) {
	for _, code := range []int{200, 201, 204} {
		resp := &http.Response{
			StatusCode: code,
			Body:       http.NoBody,
		}
		if err := checkResponse(resp); err != nil {
			t.Errorf("code %d: unexpected error: %v", code, err)
		}
	}
}

func TestCheckResponse_Error(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("page not found"))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	err := c.doJSON(http.MethodGet, ts.URL+"/rest/api/content/999", nil, nil)
	if err == nil {
		t.Fatal("expected error for 404")
	}

	ae, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if ae.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", ae.StatusCode)
	}
	if ae.Body != "page not found" {
		t.Errorf("Body = %q", ae.Body)
	}
}
