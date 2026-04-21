package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetAttachments(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/child/attachment") {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{
			"results":[
				{"id":"att1","title":"diagram.png","_links":{"download":"/download/attachments/100/diagram.png"}},
				{"id":"att2","title":"photo.jpg","_links":{"download":"/download/attachments/100/photo.jpg"}}
			]
		}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	atts, err := c.GetAttachments("100", "")
	if err != nil {
		t.Fatalf("GetAttachments: %v", err)
	}
	if len(atts) != 2 {
		t.Fatalf("len = %d, want 2", len(atts))
	}
	if atts[0].ID != "att1" || atts[0].Filename != "diagram.png" {
		t.Errorf("att[0] = %+v", atts[0])
	}
	if atts[0].DownloadPath != "/download/attachments/100/diagram.png" {
		t.Errorf("download path = %q", atts[0].DownloadPath)
	}
	if atts[1].Filename != "photo.jpg" {
		t.Errorf("att[1] = %+v", atts[1])
	}
}

func TestGetAttachments_WithFilename(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"results":[{"id":"att1","title":"specific.png","_links":{"download":"/dl"}}]}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	atts, err := c.GetAttachments("100", "specific.png")
	if err != nil {
		t.Fatalf("GetAttachments: %v", err)
	}
	if !strings.Contains(gotQuery, "filename=specific.png") {
		t.Errorf("query = %q, want filename parameter", gotQuery)
	}
	if len(atts) != 1 {
		t.Errorf("len = %d", len(atts))
	}
}

func TestGetAttachments_Empty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[]}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	atts, err := c.GetAttachments("100", "")
	if err != nil {
		t.Fatalf("GetAttachments: %v", err)
	}
	if len(atts) != 0 {
		t.Errorf("len = %d, want 0", len(atts))
	}
}

func TestDownloadAttachment(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/download/attachments/100/img.png") {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte("PNG-BINARY-DATA"))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	data, err := c.DownloadAttachment("/download/attachments/100/img.png")
	if err != nil {
		t.Fatalf("DownloadAttachment: %v", err)
	}
	if string(data) != "PNG-BINARY-DATA" {
		t.Errorf("data = %q", string(data))
	}
}

func TestDownloadAttachment_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	_, err := c.DownloadAttachment("/download/attachments/100/missing.png")
	if !IsNotFound(err) {
		t.Errorf("expected not-found, got %v", err)
	}
}

func TestUploadAttachment(t *testing.T) {
	var gotContentType string
	var gotAtlassianToken string
	var gotBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		gotContentType = r.Header.Get("Content-Type")
		gotAtlassianToken = r.Header.Get("X-Atlassian-Token")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[]}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	err := c.UploadAttachment("100", "test.png", []byte("file-data"))
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}

	// Content-Type should be multipart/form-data.
	if !strings.HasPrefix(gotContentType, "multipart/form-data") {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	// X-Atlassian-Token header must be set.
	if gotAtlassianToken != "no-check" {
		t.Errorf("X-Atlassian-Token = %q", gotAtlassianToken)
	}
	// Body should contain the filename and data.
	body := string(gotBody)
	if !strings.Contains(body, "test.png") {
		t.Error("body should contain filename")
	}
	if !strings.Contains(body, "file-data") {
		t.Error("body should contain file data")
	}
}

func TestUploadAttachment_Error(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		w.Write([]byte("too large"))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	err := c.UploadAttachment("100", "big.zip", []byte("x"))
	if err == nil {
		t.Fatal("expected error")
	}
	ae, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if ae.StatusCode != 413 {
		t.Errorf("status = %d", ae.StatusCode)
	}
}
