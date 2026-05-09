package traefik

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client is a thin HTTP client over Traefik's admin API. We use it for
// healthcheck (does Traefik answer?) and verification (are the routes we
// wrote actually loaded?). Read-only — Traefik's API does not accept route
// edits. Authenticates with basic auth so other containers on the
// locorum-global network can't read or list routes.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
}

func NewClient(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  baseURL,
		username: username,
		password: password,
		http:     &http.Client{Timeout: 5 * time.Second},
	}
}

// HTTPRouter is one element of GET /api/http/routers.
type HTTPRouter struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	Service string   `json:"service"`
	Rule    string   `json:"rule"`
	Errors  []string `json:"error,omitempty"`
}

// HTTPRouters lists all HTTP routers loaded by Traefik across all providers.
func (c *Client) HTTPRouters(ctx context.Context) ([]HTTPRouter, error) {
	var routers []HTTPRouter
	if err := c.getJSON(ctx, "/api/http/routers", &routers); err != nil {
		return nil, err
	}
	return routers, nil
}

func (c *Client) getJSON(ctx context.Context, path string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, http.NoBody)
	if err != nil {
		return err
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("traefik api %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}
