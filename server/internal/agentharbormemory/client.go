package agentharbormemory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client keeps MemoryCore credentials on the server side of AgentHarbor.
type Client struct {
	baseURL   string
	token     string
	serviceID string
	http      *http.Client
}

type Snapshot struct {
	ProjectID string `json:"project_id"`
	Version   string `json:"version"`
	Content   string `json:"content"`
}

func NewClient(baseURL, token, serviceID string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || serviceID == "" {
		return nil, errors.New("MemoryCore URL and service ID are required")
	}
	return &Client{
		baseURL:   baseURL,
		token:     token,
		serviceID: serviceID,
		http:      &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (c *Client) WriteCore(ctx context.Context, projectID, content string) error {
	if strings.TrimSpace(content) == "" {
		return errors.New("core memory content is empty")
	}
	var response struct{}
	return c.call(ctx, "/v3/core/write", projectID, map[string]string{"content": content}, &response)
}

func (c *Client) ReadCore(ctx context.Context, projectID string) (Snapshot, error) {
	var data struct {
		Content *string `json:"content"`
	}
	if err := c.call(ctx, "/v3/core/read", projectID, nil, &data); err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{ProjectID: projectID}
	if data.Content != nil {
		snapshot.Content = *data.Content
		hash := sha256.Sum256([]byte(snapshot.Content))
		snapshot.Version = hex.EncodeToString(hash[:])
	}
	return snapshot, nil
}

func (c *Client) call(ctx context.Context, endpoint, projectID string, fields map[string]string, result any) error {
	if strings.TrimSpace(projectID) == "" {
		return errors.New("project ID is required")
	}
	body := map[string]string{
		"team_id":  "ah-project-" + projectID,
		"agent_id": "ah-shared-" + projectID,
		"user_id":  "ah-project",
	}
	for key, value := range fields {
		body[key] = value
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-tdai-service-id", c.serviceID)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("decode MemoryCore response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || envelope.Code != 0 {
		return fmt.Errorf("MemoryCore %s returned HTTP %d, code %d: %s", endpoint, resp.StatusCode, envelope.Code, envelope.Message)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("MemoryCore response has no data")
	}
	if err := json.Unmarshal(envelope.Data, result); err != nil {
		return fmt.Errorf("decode MemoryCore data: %w", err)
	}
	return nil
}
