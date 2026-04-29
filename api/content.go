package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/swill/gfl/tree"
)

// pageResponse is the JSON shape returned by GET /rest/api/content/{id}.
type pageResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Title   string `json:"title"`
	Version struct {
		Number int `json:"number"`
	} `json:"version"`
	Space struct {
		Key string `json:"key"`
	} `json:"space"`
	Ancestors []struct {
		ID string `json:"id"`
	} `json:"ancestors"`
	Body struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
}

// toCfNode converts a pageResponse to a tree.CfNode.
func (p *pageResponse) toCfNode() *tree.CfNode {
	node := &tree.CfNode{
		PageID:   p.ID,
		Title:    p.Title,
		SpaceKey: p.Space.Key,
		Body:     p.Body.Storage.Value,
		Version:  p.Version.Number,
	}
	// The last ancestor is the direct parent.
	if len(p.Ancestors) > 0 {
		node.ParentPageID = p.Ancestors[len(p.Ancestors)-1].ID
	}
	return node
}

// childrenResponse is the JSON shape returned by GET /rest/api/content/{id}/child/page.
type childrenResponse struct {
	Results []pageResponse `json:"results"`
	Size    int            `json:"size"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// GetPage fetches a single page with body, version, ancestors, and space expanded.
func (c *Client) GetPage(pageID string) (*tree.CfNode, error) {
	url := fmt.Sprintf("%s/rest/api/content/%s?expand=body.storage,version,ancestors,space", c.baseURL, pageID)
	var resp pageResponse
	if err := c.doJSON(http.MethodGet, url, nil, &resp); err != nil {
		return nil, err
	}
	return resp.toCfNode(), nil
}

// GetChildren fetches one level of child pages for a parent.
// Bodies are not expanded — use GetPage on individual children if content is needed.
func (c *Client) GetChildren(parentID string) ([]*tree.CfNode, error) {
	var all []*tree.CfNode
	start := 0
	limit := 200

	for {
		url := fmt.Sprintf("%s/rest/api/content/%s/child/page?limit=%d&start=%d&expand=version,ancestors,space",
			c.baseURL, parentID, limit, start)

		var resp childrenResponse
		if err := c.doJSON(http.MethodGet, url, nil, &resp); err != nil {
			return nil, err
		}

		for i := range resp.Results {
			all = append(all, resp.Results[i].toCfNode())
		}

		if resp.Links.Next == "" {
			break
		}
		start += resp.Size
	}

	return all, nil
}

// FetchTree fetches the full page hierarchy rooted at rootID via BFS.
// If fetchBody is true, each page is fetched individually with body content
// expanded (slower but needed for init). If false, only structure is fetched.
func (c *Client) FetchTree(rootID string, fetchBody bool) (*tree.CfTree, error) {
	root, err := c.GetPage(rootID)
	if err != nil {
		return nil, fmt.Errorf("fetch root %s: %w", rootID, err)
	}
	ct := tree.NewCfTree(root)

	// BFS: fetch children level by level.
	queue := []string{rootID}
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]

		children, err := c.GetChildren(parentID)
		if err != nil {
			return nil, fmt.Errorf("fetch children of %s: %w", parentID, err)
		}

		for _, child := range children {
			if fetchBody {
				full, err := c.GetPage(child.PageID)
				if err != nil {
					return nil, fmt.Errorf("fetch page %s: %w", child.PageID, err)
				}
				full.ParentPageID = parentID
				ct.Add(full)
			} else {
				child.ParentPageID = parentID
				ct.Add(child)
			}
			queue = append(queue, child.PageID)
		}
	}

	return ct, nil
}

// createPageRequest is the JSON body for POST /rest/api/content.
type createPageRequest struct {
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Ancestors []ancestorRef  `json:"ancestors"`
	Space     spaceRef       `json:"space"`
	Body      createPageBody `json:"body"`
}

type ancestorRef struct {
	ID string `json:"id"`
}

type spaceRef struct {
	Key string `json:"key"`
}

type createPageBody struct {
	Storage storageValue `json:"storage"`
}

type storageValue struct {
	Value          string `json:"value"`
	Representation string `json:"representation"`
}

// CreatePage creates a new page under the given parent.
// Returns the new page as a CfNode (with the assigned page ID and version).
func (c *Client) CreatePage(spaceKey, parentID, title, storageXML string) (*tree.CfNode, error) {
	payload := createPageRequest{
		Type:      "page",
		Title:     title,
		Ancestors: []ancestorRef{{ID: parentID}},
		Space:     spaceRef{Key: spaceKey},
		Body: createPageBody{
			Storage: storageValue{
				Value:          storageXML,
				Representation: "storage",
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/rest/api/content", c.baseURL)
	var resp pageResponse
	if err := c.doJSON(http.MethodPost, url, bytes.NewReader(data), &resp); err != nil {
		return nil, err
	}

	node := resp.toCfNode()
	node.ParentPageID = parentID
	return node, nil
}

// updatePageRequest is the JSON body for PUT /rest/api/content/{id}.
type updatePageRequest struct {
	Version   versionRef     `json:"version"`
	Title     string         `json:"title"`
	Type      string         `json:"type"`
	Ancestors []ancestorRef  `json:"ancestors,omitempty"`
	Body      createPageBody `json:"body"`
}

type versionRef struct {
	Number int `json:"number"`
}

// UpdatePage updates the title, body, and optionally the parent of an existing page.
// The version must be the current version + 1. Pass an empty parentID to leave
// the parent unchanged (ancestors will be omitted from the request).
func (c *Client) UpdatePage(pageID string, version int, title, storageXML, parentID string) error {
	payload := updatePageRequest{
		Version: versionRef{Number: version},
		Title:   title,
		Type:    "page",
		Body: createPageBody{
			Storage: storageValue{
				Value:          storageXML,
				Representation: "storage",
			},
		},
	}
	if parentID != "" {
		payload.Ancestors = []ancestorRef{{ID: parentID}}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/rest/api/content/%s", c.baseURL, pageID)
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkResponse(resp)
}

// DeletePage deletes a page by ID. Confluence cascades the delete to descendants.
func (c *Client) DeletePage(pageID string) error {
	url := fmt.Sprintf("%s/rest/api/content/%s", c.baseURL, pageID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Confluence returns 204 No Content on successful delete.
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return checkResponse(resp)
}

// restURL builds a full REST API URL from a path suffix.
// Used internally to avoid string formatting in multiple places.
func (c *Client) restURL(pathSuffix string) string {
	return c.baseURL + "/rest/api" + pathSuffix
}

// SetHTTPClient replaces the underlying HTTP client. Useful for testing
// with httptest.NewServer.
func (c *Client) SetHTTPClient(hc *http.Client) {
	c.http = hc
}

// trimNextLink extracts the path portion from a _links.next value.
// Confluence returns paths like "/rest/api/content/123/child/page?limit=200&start=200".
func trimNextLink(next, baseURL string) string {
	return strings.TrimPrefix(next, baseURL)
}
