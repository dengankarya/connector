package payment

import "context"

type XenPlatformClient interface {
	CreateAccount(ctx context.Context, req CreateAccountRequest) (*Account, error)
	GetAccount(ctx context.Context, id string) (*Account, error)
}

type PaymentService struct {
	xenplatform XenPlatformClient
}

func NewPaymentService(xenplatform XenPlatformClient) *PaymentService {
	return &PaymentService{xenplatform: xenplatform}
}

func (s *PaymentService) CreateAccount(ctx context.Context, req CreateAccountRequest) (*Account, error) {
	return s.xenplatform.CreateAccount(ctx, req)
}

func (s *PaymentService) GetAccount(ctx context.Context, id string) (*Account, error) {
	return s.xenplatform.GetAccount(ctx, id)
}
