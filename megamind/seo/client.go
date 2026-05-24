package seo

// Client — base HTTP client for sidecar communication.
// This is the core client that schema_push_client.go wraps.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is the base HTTP client for the sidecar.
type Client struct {
	BaseURL    string
	AuthToken  string
	HTTPClient *http.Client
}

// SchemaUpdate represents a schema push payload.
type SchemaUpdate struct {
	Site     string `json:"site"`
	JSONLD   string `json:"jsonld"`
	PushedBy string `json:"pushed_by"`
}

// BulkUpdate represents a bulk push payload.
type BulkUpdate struct {
	Schemas []SchemaUpdate `json:"schemas"`
}

// PushResult represents a push response.
type PushResult struct {
	Status    string `json:"status"`
	Site      string `json:"site"`
	Version   int    `json:"version"`
	UpdatedAt string `json:"updated_at"`
}

// SiteStatus represents status for a single site.
type SiteStatus struct {
	Site      string `json:"site"`
	UpdatedAt string `json:"updated_at"`
	PushedBy  string `json:"pushed_by"`
	Version   int    `json:"version"`
	SizeBytes int    `json:"size_bytes"`
}

// StatusResponse represents the sidecar status response.
type StatusResponse struct {
	Status     string       `json:"status"`
	TotalSites int          `json:"total_sites"`
	Sites      []SiteStatus `json:"sites"`
}

// NewClient creates a new sidecar client.
func NewClient(baseURL, authToken string) *Client {
	return &Client{
		BaseURL:   baseURL,
		AuthToken: authToken,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Push sends a single site schema to the sidecar.
func (c *Client) Push(site, jsonld string) (*PushResult, error) {
	payload := SchemaUpdate{
		Site:     site,
		JSONLD:   jsonld,
		PushedBy: "megamind",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/update/%s", c.BaseURL, site), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sidecar returned %d: %s", resp.StatusCode, string(b))
	}

	var result PushResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &result, nil
}

// PushAll sends all schemas in a single bulk request.
func (c *Client) PushAll(schemas map[string]string) error {
	bulk := BulkUpdate{}
	for site, jsonld := range schemas {
		bulk.Schemas = append(bulk.Schemas, SchemaUpdate{
			Site:     site,
			JSONLD:   jsonld,
			PushedBy: "megamind",
		})
	}

	body, err := json.Marshal(bulk)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/update-bulk", c.BaseURL), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sidecar returned %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// Status checks what schemas are live on the sidecar.
func (c *Client) Status() (*StatusResponse, error) {
	resp, err := c.HTTPClient.Get(fmt.Sprintf("%s/status", c.BaseURL))
	if err != nil {
		return nil, fmt.Errorf("get: %w", err)
	}
	defer resp.Body.Close()

	var status StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &status, nil
}
