package durianpay_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/dengankarya/connector/pkg/durianpay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(server *httptest.Server) *durianpay.Client {
	return durianpay.NewClient("test-key", server.URL, nil)
}

func TestClient_CreateRefund_Fresh(t *testing.T) {
	var postCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/refunds/payment/pay-abc":
			// No existing refunds.
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/refunds":
			postCalled = true
			json.NewEncoder(w).Encode(map[string]any{
				"id":     "rfn-001",
				"ref_id": "refund:txn-123",
				"amount": "100000",
				"status": "approved",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestClient(server)
	refund, err := client.CreateRefund(t.Context(), provider.CreateRefundRequest{
		ProviderPaymentID: "pay-abc",
		Amount:            100000,
		Reason:            "customer request",
		ExternalID:        "refund:txn-123",
	})

	require.NoError(t, err)
	assert.True(t, postCalled, "POST /v1/refunds should have been called")
	assert.Equal(t, "rfn-001", refund.ProviderRefundID)
	assert.Equal(t, "refund:txn-123", refund.ExternalID)
	assert.Equal(t, int64(100000), refund.Amount)
	assert.Equal(t, "approved", refund.Status)
}

func TestClient_CreateRefund_Idempotent(t *testing.T) {
	var postCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/refunds/payment/pay-abc":
			// Existing refund with matching ref_id.
			json.NewEncoder(w).Encode(map[string]any{
				"data": []any{
					map[string]any{
						"id":     "rfn-001",
						"ref_id": "refund:txn-123",
						"amount": "100000",
						"status": "approved",
					},
				},
			})
		case r.Method == http.MethodPost:
			postCalled = true
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := newTestClient(server)
	refund, err := client.CreateRefund(t.Context(), provider.CreateRefundRequest{
		ProviderPaymentID: "pay-abc",
		Amount:            100000,
		ExternalID:        "refund:txn-123",
	})

	require.NoError(t, err)
	assert.False(t, postCalled, "POST should NOT have been called — idempotent path")
	assert.Equal(t, "rfn-001", refund.ProviderRefundID)
}

func TestClient_CreateRefund_ProviderForbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"message":"refund amount exceeds original payment"}`))
		}
	}))
	defer server.Close()

	client := newTestClient(server)
	_, err := client.CreateRefund(t.Context(), provider.CreateRefundRequest{
		ProviderPaymentID: "pay-abc",
		Amount:            999999,
		ExternalID:        "refund:txn-bad",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}

func TestClient_GetRefundsByPaymentID_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/refunds/payment/pay-xyz", r.URL.Path)
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer server.Close()

	client := newTestClient(server)
	refunds, err := client.GetRefundsByPaymentID(t.Context(), "pay-xyz")

	require.NoError(t, err)
	assert.Empty(t, refunds)
}
