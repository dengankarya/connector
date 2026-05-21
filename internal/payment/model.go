package payment

type PublicProfile struct {
	BusinessName string `json:"business_name,omitempty"`
}

type Account struct {
	ID            string        `json:"id"`
	Email         string        `json:"email"`
	Type          string        `json:"type"`
	PublicProfile PublicProfile `json:"public_profile"`
	Status        string        `json:"status"`
	Country       string        `json:"country"`
	Created       string        `json:"created"`
	Updated       string        `json:"updated"`
}

type CreateAccountRequest struct {
	Email         string        `json:"email"`
	Type          string        `json:"type"`
	PublicProfile PublicProfile `json:"public_profile"`
}

type WebhookEvent struct {
	Event      string `json:"event"`
	BusinessID string `json:"business_id"`
	Created    string `json:"created"`
	Data       any    `json:"data"`
}
