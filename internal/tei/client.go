// Package tei wraps the Hugging Face text-embeddings-inference HTTP API.
// TEI returns a 2D array of float32 vectors — one per input string.
package tei

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxBatchSize matches TEI's default --max-client-batch-size. Requests larger
// than this are split into sub-batches by Embed.
const maxBatchSize = 32

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Embed sends inputs to TEI's /embed endpoint and returns a vector per input,
// chunking into sub-batches of maxBatchSize to stay under TEI's server limit.
// Empty input → empty output without an HTTP call.
func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += maxBatchSize {
		end := start + maxBatchSize
		if end > len(inputs) {
			end = len(inputs)
		}
		chunk, err := c.embedChunk(ctx, inputs[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, chunk...)
	}
	return out, nil
}

func (c *Client) embedChunk(ctx context.Context, inputs []string) ([][]float32, error) {
	body, err := json.Marshal(struct {
		Inputs []string `json:"inputs"`
	}{Inputs: inputs})
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tei %d: %s", resp.StatusCode, string(b))
	}
	var out [][]float32
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return out, nil
}
