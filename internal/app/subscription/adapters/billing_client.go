package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/contracts"
	"github.com/wuyiadepoju/subscription-management/internal/app/subscription/domain"
)

var _ contracts.BillingClient = (*HTTPBillingClient)(nil)

// HTTPBillingClient implements the billing client interface using HTTP
type HTTPBillingClient struct {
	client  *http.Client
	baseURL string
}

// NewHTTPBillingClient creates a new HTTP billing client
func NewHTTPBillingClient(client *http.Client, baseURL string) *HTTPBillingClient {
	return &HTTPBillingClient{
		client:  client,
		baseURL: baseURL,
	}
}

// ValidateCustomer validates a customer with the external billing API
func (c *HTTPBillingClient) ValidateCustomer(ctx context.Context, customerID string) error {
	url := fmt.Sprintf("%s/validate/%s", c.baseURL, customerID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to validate customer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return domain.ErrInvalidCustomer
	}

	var result struct {
		Valid bool `json:"valid"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if !result.Valid {
		return domain.ErrInvalidCustomer
	}

	return nil
}

// ProcessRefund processes a refund through the external billing API
func (c *HTTPBillingClient) ProcessRefund(ctx context.Context, amount int64) error {
	url := fmt.Sprintf("%s/refund", c.baseURL)

	payload := map[string]any{
		"amount": amount,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to process refund: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("refund failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}
