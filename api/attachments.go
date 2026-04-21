package api

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

// Attachment represents a Confluence attachment on a page.
type Attachment struct {
	ID           string
	Filename     string
	DownloadPath string // relative path for download, e.g. "/download/attachments/123/file.png"
}

// attachmentResponse is the JSON shape returned by GET .../child/attachment.
type attachmentResponse struct {
	Results []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Links struct {
			Download string `json:"download"`
		} `json:"_links"`
	} `json:"results"`
}

// GetAttachments lists attachments for a page. If filename is non-empty,
// only the attachment with that exact filename is returned.
func (c *Client) GetAttachments(pageID, filename string) ([]Attachment, error) {
	url := fmt.Sprintf("%s/rest/api/content/%s/child/attachment", c.baseURL, pageID)
	if filename != "" {
		url += "?filename=" + filename
	}

	var resp attachmentResponse
	if err := c.doJSON(http.MethodGet, url, nil, &resp); err != nil {
		return nil, err
	}

	var out []Attachment
	for _, r := range resp.Results {
		out = append(out, Attachment{
			ID:           r.ID,
			Filename:     r.Title,
			DownloadPath: r.Links.Download,
		})
	}
	return out, nil
}

// DownloadAttachment downloads an attachment by its download path.
// The path should be the DownloadPath from an Attachment value.
func (c *Client) DownloadAttachment(downloadPath string) ([]byte, error) {
	url := c.baseURL + downloadPath
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	return io.ReadAll(resp.Body)
}

// UploadAttachment uploads a file as an attachment to the given page.
// If an attachment with the same filename already exists, Confluence
// creates a new version of it.
func (c *Client) UploadAttachment(pageID, filename string, data []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	url := fmt.Sprintf("%s/rest/api/content/%s/child/attachment", c.baseURL, pageID)
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Atlassian-Token", "no-check")

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkResponse(resp)
}
