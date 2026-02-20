package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2/google"
)

// CreditsSummary represents GCP credits usage summary
type CreditsSummary struct {
	TotalCredits    float64
	UsedAmount      float64
	RemainingAmount float64
	RemainingPct    float64
	QueryTime       time.Time
}

// BillingClient wraps GCP Cloud Billing API calls
type BillingClient struct {
	billingAccountID string
	credentials      *google.Credentials
	httpClient       *http.Client
}

// NewBillingClient creates a GCP billing client using service account JSON or file path
func NewBillingClient(serviceAccountJSONOrPath, billingAccountID string) (*BillingClient, error) {
	jsonData := []byte(serviceAccountJSONOrPath)
	// If it doesn't look like JSON, treat as file path
	if !strings.HasPrefix(strings.TrimSpace(serviceAccountJSONOrPath), "{") {
		var err error
		jsonData, err = os.ReadFile(serviceAccountJSONOrPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read service account file: %w", err)
		}
	}

	ctx := context.Background()
	creds, err := google.CredentialsFromJSON(ctx, jsonData,
		"https://www.googleapis.com/auth/cloud-billing.readonly",
		"https://www.googleapis.com/auth/cloud-platform",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to parse service account JSON: %w", err)
	}

	return &BillingClient{
		billingAccountID: billingAccountID,
		credentials:      creds,
		httpClient:       &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// doRequest executes an authenticated HTTP request
func (c *BillingClient) doRequest(url string) ([]byte, int, error) {
	token, err := c.credentials.TokenSource.Token()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get token: %w", err)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode, nil
}

// QueryCostSummary queries billing account cost and calculates credits remaining
func (c *BillingClient) QueryCostSummary(totalCredits float64) (*CreditsSummary, error) {
	usedAmount, err := c.queryTotalCost()
	if err != nil {
		return nil, err
	}

	remaining := totalCredits - usedAmount
	if remaining < 0 {
		remaining = 0
	}
	pct := 0.0
	if totalCredits > 0 {
		pct = remaining / totalCredits * 100
	}

	return &CreditsSummary{
		TotalCredits:    totalCredits,
		UsedAmount:      usedAmount,
		RemainingAmount: remaining,
		RemainingPct:    pct,
		QueryTime:       time.Now(),
	}, nil
}

// queryTotalCost tries API endpoints to get total cost
func (c *BillingClient) queryTotalCost() (float64, error) {
	// Try v1beta1 cost summary API
	url := fmt.Sprintf(
		"https://cloudbilling.googleapis.com/v1beta1/billingAccounts/%s/services/-/costs:summarize",
		c.billingAccountID,
	)

	body, status, err := c.doRequest(url)
	if err != nil {
		return 0, fmt.Errorf("failed to query costs: %w", err)
	}

	log.Debugf("GCP cost API response status=%d: %s", status, string(body))

	if status == http.StatusOK {
		if amount := extractTotalCost(body); amount > 0 {
			return amount, nil
		}
	}

	// Fallback: verify billing account credentials
	infoURL := fmt.Sprintf("https://cloudbilling.googleapis.com/v1/billingAccounts/%s", c.billingAccountID)
	infoBody, infoStatus, err := c.doRequest(infoURL)
	if err != nil {
		return 0, fmt.Errorf("failed to query billing account: %w", err)
	}

	if infoStatus != http.StatusOK {
		return 0, fmt.Errorf("billing API status %d: %s", infoStatus, string(infoBody))
	}

	log.Debugf("Billing account info: %s", string(infoBody))
	return 0, fmt.Errorf("cost query API not available (v1beta1 status %d), billing account verified", status)
}

// extractTotalCost extracts total cost from API JSON response
func extractTotalCost(data []byte) float64 {
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0
	}

	if cost, ok := result["cost"].(map[string]interface{}); ok {
		if amount, ok := cost["amount"].(float64); ok {
			return amount
		}
	}

	if costs, ok := result["costs"].([]interface{}); ok {
		var total float64
		for _, c := range costs {
			if m, ok := c.(map[string]interface{}); ok {
				if a, ok := m["amount"].(float64); ok {
					total += a
				}
			}
		}
		return total
	}

	return 0
}
