package xenplatform

type publicProfileDTO struct {
	BusinessName string `json:"business_name,omitempty"`
}

type accountDTO struct {
	ID            string           `json:"id"`
	Email         string           `json:"email"`
	Type          string           `json:"type"`
	PublicProfile publicProfileDTO `json:"public_profile"`
	Status        string           `json:"status"`
	Country       string           `json:"country"`
	Created       string           `json:"created"`
	Updated       string           `json:"updated"`
}

type createAccountRequestDTO struct {
	Email         string           `json:"email"`
	Type          string           `json:"type"`
	PublicProfile publicProfileDTO `json:"public_profile"`
}
