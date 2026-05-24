package seo

// schema_push_client.go — enhanced push client for adaptive loop
// Wraps the basic Client with retry logic and response tracking.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// PushClient handles schema pushes to the sidecar.
type PushClient struct {
	*Client
	maxRetries int
	retryDelay time.Duration
}

// NewPushClient creates an enhanced push client.
func NewPushClient(baseURL, authToken string) *PushClient {
	return &PushClient{
		Client:     NewClient(baseURL, authToken),
		maxRetries: 3,
		retryDelay: time.Second,
	}
}

// PushCandidate pushes a schema candidate to the sidecar.
func (c *PushClient) PushCandidate(candidate *SchemaCandidate) (*PushResult, error) {
	var lastErr error

	for attempt := 0; attempt < c.maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(c.retryDelay * time.Duration(attempt))
		}

		result, err := c.Push(candidate.SchemaKey, candidate.JSONLD)
		if err == nil {
			return result, nil
		}

		lastErr = err
	}

	return nil, fmt.Errorf("push failed after %d attempts: %w", c.maxRetries, lastErr)
}

// PushBatch pushes multiple candidates in a single request.
func (c *PushClient) PushBatch(candidates []*SchemaCandidate) (*BulkPushResult, error) {
	schemas := make(map[string]string)
	for _, candidate := range candidates {
		schemas[candidate.SchemaKey] = candidate.JSONLD
	}

	if err := c.PushAll(schemas); err != nil {
		return nil, err
	}

	return &BulkPushResult{
		Success: len(candidates),
		Failed:  0,
	}, nil
}

// BulkPushResult contains bulk push outcome.
type BulkPushResult struct {
	Success int
	Failed  int
	Results []PushResult
}

// FetchCurrentSchema retrieves the current schema from sidecar.
func (c *PushClient) FetchCurrentSchema(schemaKey string) (string, error) {
	url := fmt.Sprintf("%s/schema/%s", c.BaseURL, schemaKey)

	resp, err := c.HTTPClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return "", nil // No schema exists
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("sidecar returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	return string(body), nil
}

// DeleteSchema removes a schema from the sidecar.
func (c *PushClient) DeleteSchema(schemaKey, authToken string) error {
	url := fmt.Sprintf("%s/schema/%s", c.BaseURL, schemaKey)

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+authToken)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete failed: %d - %s", resp.StatusCode, string(body))
	}

	return nil
}

// HealthCheck verifies sidecar is responsive.
func (c *PushClient) HealthCheck() error {
	resp, err := c.HTTPClient.Get(fmt.Sprintf("%s/status", c.BaseURL))
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("sidecar unhealthy: %d", resp.StatusCode)
	}

	return nil
}

// PushWithCallback pushes and calls back with result.
func (c *PushClient) PushWithCallback(
	candidate *SchemaCandidate,
	onSuccess func(*PushResult),
	onError func(error),
) {
	result, err := c.PushCandidate(candidate)
	if err != nil {
		if onError != nil {
			onError(err)
		}
		return
	}

	if onSuccess != nil {
		onSuccess(result)
	}
}

// --- Batch operations ---

// PushBulkRequest represents a bulk push payload.
type PushBulkRequest struct {
	Schemas []SchemaUpdate `json:"schemas"`
}

// PushBulkWithDetails pushes multiple schemas and returns detailed results.
func (c *PushClient) PushBulkWithDetails(candidates []*SchemaCandidate) (*BulkPushResult, error) {
	if len(candidates) == 0 {
		return &BulkPushResult{}, nil
	}

	req := PushBulkRequest{}
	for _, candidate := range candidates {
		req.Schemas = append(req.Schemas, SchemaUpdate{
			Site:     candidate.SchemaKey,
			JSONLD:   candidate.JSONLD,
			PushedBy: "megamind-adaptive",
		})
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequest("POST", fmt.Sprintf("%s/update-bulk", c.BaseURL), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.AuthToken)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bulk push failed: %d - %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Status  string       `json:"status"`
		Updated int          `json:"updated"`
		Results []PushResult `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	return &BulkPushResult{
		Success: result.Updated,
		Failed:  len(candidates) - result.Updated,
		Results: result.Results,
	}, nil
}
