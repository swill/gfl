package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestPage returns a JSON string for a pageResponse with the given fields.
func newTestPage(id, title, spaceKey, body string, version int, parentID string) string {
	ancestors := "[]"
	if parentID != "" {
		ancestors = `[{"id":"` + parentID + `"}]`
	}
	return `{
		"id":"` + id + `",
		"type":"page",
		"title":"` + title + `",
		"version":{"number":` + jsonInt(version) + `},
		"space":{"key":"` + spaceKey + `"},
		"ancestors":` + ancestors + `,
		"body":{"storage":{"value":"` + body + `","representation":"storage"}}
	}`
}

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestGetPage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/rest/api/content/123") {
			t.Errorf("path = %s", r.URL.Path)
		}
		if !strings.Contains(r.URL.RawQuery, "expand=body.storage,version,ancestors,space") {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Write([]byte(newTestPage("123", "Root Page", "DOCS", "<p>hello</p>", 5, "")))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	node, err := c.GetPage("123")
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	if node.PageID != "123" {
		t.Errorf("PageID = %q", node.PageID)
	}
	if node.Title != "Root Page" {
		t.Errorf("Title = %q", node.Title)
	}
	if node.SpaceKey != "DOCS" {
		t.Errorf("SpaceKey = %q", node.SpaceKey)
	}
	if node.Body != "<p>hello</p>" {
		t.Errorf("Body = %q", node.Body)
	}
	if node.Version != 5 {
		t.Errorf("Version = %d", node.Version)
	}
	if node.ParentPageID != "" {
		t.Errorf("ParentPageID = %q, want empty for root", node.ParentPageID)
	}
}

func TestGetPage_WithParent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(newTestPage("456", "Child", "DOCS", "", 2, "123")))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	node, err := c.GetPage("456")
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	if node.ParentPageID != "123" {
		t.Errorf("ParentPageID = %q, want 123", node.ParentPageID)
	}
}

func TestGetPage_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	_, err := c.GetPage("999")
	if !IsNotFound(err) {
		t.Errorf("expected not-found, got %v", err)
	}
}

func TestGetChildren_SinglePage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"results":[` + newTestPage("200", "Child A", "DOCS", "", 1, "100") + `],
			"size":1,
			"_links":{}
		}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	children, err := c.GetChildren("100")
	if err != nil {
		t.Fatalf("GetChildren: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("len = %d, want 1", len(children))
	}
	if children[0].PageID != "200" {
		t.Errorf("PageID = %q", children[0].PageID)
	}
	if children[0].Title != "Child A" {
		t.Errorf("Title = %q", children[0].Title)
	}
}

func TestGetChildren_Pagination(t *testing.T) {
	call := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		switch call {
		case 1:
			w.Write([]byte(`{
				"results":[` + newTestPage("200", "A", "DOCS", "", 1, "100") + `],
				"size":1,
				"_links":{"next":"/rest/api/content/100/child/page?limit=200&start=1"}
			}`))
		case 2:
			w.Write([]byte(`{
				"results":[` + newTestPage("300", "B", "DOCS", "", 1, "100") + `],
				"size":1,
				"_links":{}
			}`))
		default:
			t.Error("unexpected request")
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	children, err := c.GetChildren("100")
	if err != nil {
		t.Fatalf("GetChildren: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("len = %d, want 2", len(children))
	}
	if children[0].PageID != "200" || children[1].PageID != "300" {
		t.Errorf("children: %v, %v", children[0].PageID, children[1].PageID)
	}
	if call != 2 {
		t.Errorf("expected 2 requests, got %d", call)
	}
}

func TestFetchTree_NoBodies(t *testing.T) {
	// Root 100 → Child 200 → Grandchild 300
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/content/100") && !strings.Contains(path, "/child"):
			w.Write([]byte(newTestPage("100", "Root", "DOCS", "<p>root</p>", 3, "")))
		case strings.HasSuffix(path, "/content/100/child/page"):
			w.Write([]byte(`{"results":[` + newTestPage("200", "Child", "DOCS", "", 1, "100") + `],"size":1,"_links":{}}`))
		case strings.HasSuffix(path, "/content/200/child/page"):
			w.Write([]byte(`{"results":[` + newTestPage("300", "Grandchild", "DOCS", "", 1, "200") + `],"size":1,"_links":{}}`))
		case strings.HasSuffix(path, "/content/300/child/page"):
			w.Write([]byte(`{"results":[],"size":0,"_links":{}}`))
		default:
			t.Errorf("unexpected path: %s", path)
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	ct, err := c.FetchTree("100", false)
	if err != nil {
		t.Fatalf("FetchTree: %v", err)
	}
	if ct.Size() != 3 {
		t.Errorf("size = %d, want 3", ct.Size())
	}
	if ct.Root.Title != "Root" {
		t.Errorf("root title = %q", ct.Root.Title)
	}
	if ct.Root.Body != "<p>root</p>" {
		t.Errorf("root body = %q", ct.Root.Body)
	}
	// Child should have no body (fetched without expand).
	child := ct.Page("200")
	if child == nil {
		t.Fatal("child 200 not in tree")
	}
	if child.ParentPageID != "100" {
		t.Errorf("child parent = %q", child.ParentPageID)
	}
	if child.Body != "" {
		t.Errorf("child body = %q, want empty (no fetchBody)", child.Body)
	}
	// Grandchild.
	gc := ct.Page("300")
	if gc == nil {
		t.Fatal("grandchild 300 not in tree")
	}
	if gc.ParentPageID != "200" {
		t.Errorf("grandchild parent = %q", gc.ParentPageID)
	}
}

func TestFetchTree_WithBodies(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/content/100") && !strings.Contains(path, "/child"):
			w.Write([]byte(newTestPage("100", "Root", "DOCS", "<p>root</p>", 3, "")))
		case strings.HasSuffix(path, "/content/100/child/page"):
			w.Write([]byte(`{"results":[` + newTestPage("200", "Child", "DOCS", "", 1, "100") + `],"size":1,"_links":{}}`))
		case strings.HasSuffix(path, "/content/200") && !strings.Contains(path, "/child"):
			w.Write([]byte(newTestPage("200", "Child", "DOCS", "<p>child body</p>", 1, "100")))
		case strings.HasSuffix(path, "/content/200/child/page"):
			w.Write([]byte(`{"results":[],"size":0,"_links":{}}`))
		default:
			t.Errorf("unexpected path: %s", path)
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	ct, err := c.FetchTree("100", true)
	if err != nil {
		t.Fatalf("FetchTree: %v", err)
	}
	child := ct.Page("200")
	if child.Body != "<p>child body</p>" {
		t.Errorf("child body = %q, want '<p>child body</p>'", child.Body)
	}
}

func TestCreatePage(t *testing.T) {
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		// Return a response with the newly assigned ID.
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(newTestPage("999", "New Page", "DOCS", "<p>hi</p>", 1, "100")))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	node, err := c.CreatePage("DOCS", "100", "New Page", "<p>hi</p>")
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	if node.PageID != "999" {
		t.Errorf("PageID = %q", node.PageID)
	}
	if node.ParentPageID != "100" {
		t.Errorf("ParentPageID = %q", node.ParentPageID)
	}
	if node.Version != 1 {
		t.Errorf("Version = %d", node.Version)
	}

	// Verify request body structure.
	var req createPageRequest
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req.Type != "page" {
		t.Errorf("type = %q", req.Type)
	}
	if req.Title != "New Page" {
		t.Errorf("title = %q", req.Title)
	}
	if req.Space.Key != "DOCS" {
		t.Errorf("space = %q", req.Space.Key)
	}
	if len(req.Ancestors) != 1 || req.Ancestors[0].ID != "100" {
		t.Errorf("ancestors = %v", req.Ancestors)
	}
	if req.Body.Storage.Value != "<p>hi</p>" {
		t.Errorf("body = %q", req.Body.Storage.Value)
	}
}

func TestUpdatePage(t *testing.T) {
	var gotBody string
	var gotMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	err := c.UpdatePage("123", 6, "Updated Title", "<p>new</p>", "100")
	if err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s", gotMethod)
	}

	var req updatePageRequest
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Version.Number != 6 {
		t.Errorf("version = %d", req.Version.Number)
	}
	if req.Title != "Updated Title" {
		t.Errorf("title = %q", req.Title)
	}
	if len(req.Ancestors) != 1 || req.Ancestors[0].ID != "100" {
		t.Errorf("ancestors = %v", req.Ancestors)
	}
}

func TestUpdatePage_NoParent(t *testing.T) {
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	err := c.UpdatePage("123", 6, "Title", "<p>body</p>", "")
	if err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}

	// Ancestors should be omitted from JSON.
	if strings.Contains(gotBody, "ancestors") {
		t.Errorf("ancestors should be omitted when parentID is empty: %s", gotBody)
	}
}

func TestUpdatePage_Conflict(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"message":"version conflict"}`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	err := c.UpdatePage("123", 6, "Title", "<p>body</p>", "")
	if !IsConflict(err) {
		t.Errorf("expected conflict error, got %v", err)
	}
}

func TestDeletePage(t *testing.T) {
	var gotMethod string
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	err := c.DeletePage("456")
	if err != nil {
		t.Fatalf("DeletePage: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %s", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/rest/api/content/456") {
		t.Errorf("path = %s", gotPath)
	}
}

func TestDeletePage_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "t")
	c.SetHTTPClient(ts.Client())

	err := c.DeletePage("999")
	if !IsNotFound(err) {
		t.Errorf("expected not-found, got %v", err)
	}
}
