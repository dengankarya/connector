package doku

type (
	DokuCreateCheckoutRequest struct {
		Order                   DokuOrder      `json:"order"`
		Payment                 DokuPayment    `json:"payment"`
		Customer                DokuCustomer   `json:"customer"`
		AdditionalInfo          map[string]any `json:"additional_info"`
		OverrideNotificationURL string         `json:"override_notification_url,omitempty"`
		CallbackURL             string         `json:"callback_url,omitempty"`
		CallbackURLCancel       string         `json:"callback_url_cancel,omitempty"`
		CallbackURLResult       string         `json:"callback_url_result,omitempty"`
	}

	DokuOrder struct {
		Amount        int64  `json:"amount"`
		InvoiceNumber string `json:"invoice_number"`
	}

	DokuCustomer struct {
		Name  string `json:"name,omitempty"`
		Email string `json:"email,omitempty"`
		Phone string `json:"phone,omitempty"`
	}

	DokuPayment struct {
		PaymentDueDate     int      `json:"payment_due_date,omitempty"`
		Type               string   `json:"type,omitempty"`
		PaymentMethodTypes []string `json:"payment_method_types,omitempty"`
	}
)

func mapCheckoutRequest(req CheckoutRequest) DokuCreateCheckoutRequest {
	result := DokuCreateCheckoutRequest{
		Order: DokuOrder{
			Amount:        req.Amount,
			InvoiceNumber: req.InvoiceNumber,
		},
		Payment: DokuPayment{
			PaymentDueDate:     req.DueMinutes,
			Type:               req.PaymentType,
			PaymentMethodTypes: req.PaymentMethodTypes,
		},
		Customer: DokuCustomer{
			Name:  req.CustomerName,
			Email: req.CustomerEmail,
			Phone: req.CustomerPhone,
		},
		AdditionalInfo:          map[string]any{},
		OverrideNotificationURL: req.OverrideNotificationURL,
		CallbackURL:             req.CallbackURL,
		CallbackURLCancel:       req.CallbackURLCancel,
		CallbackURLResult:       req.CallbackURLResult,
	}

	if req.AccountID != "" {
		result.AdditionalInfo["account"] = map[string]any{
			"id": req.AccountID,
		}
	}

	return result
}

type (
	DokuCreateCheckoutResponse struct {
		Response DokuCreateCheckoutResponseBody `json:"response"`
	}

	DokuCreateCheckoutResponseBody struct {
		Payment DokuCreateCheckoutPaymentResponse `json:"payment"`
		Order   DokuCreateCheckoutOrderResponse   `json:"order"`
	}

	DokuCreateCheckoutPaymentResponse struct {
		URL         string `json:"url"`
		ExpiredDate string `json:"expired_date"`
	}

	DokuCreateCheckoutOrderResponse struct {
		SessionID string `json:"session_id"`
	}
)
