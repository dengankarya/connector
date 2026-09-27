package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/provider"
	providermocks "github.com/dengankarya/connector/internal/payment/provider/mocks"
	"github.com/dengankarya/connector/internal/payment/service"
	svcmocks "github.com/dengankarya/connector/internal/payment/service/mocks"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestService(t *testing.T, store service.TransactionStore, ledger service.LedgerRecorder, txRunner service.TxRunnerIface) *service.PaymentService {
	t.Helper()
	l := logrus.New()
	l.SetLevel(logrus.PanicLevel)
	return service.NewPaymentService(store, txRunner, ledger, nil, l)
}

// fakeTxRunner calls fn directly without a real DB transaction.
type fakeTxRunner struct{}

func (f *fakeTxRunner) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func paidTxn(tenantID int64, orderNumber string) *domain.PaymentTransaction {
	return &domain.PaymentTransaction{
		ID:                uuid.New(),
		TenantID:          tenantID,
		OrderNumber:       orderNumber,
		Provider:          "durianpay",
		ProviderInvoiceID: "ps-123",
		ProviderPaymentID: "pay-abc",
		Amount:            150000,
		Currency:          "IDR",
		Status:            domain.StatusPaid,
		Version:           1,
	}
}

func TestPaymentService_Refund_HappyPath(t *testing.T) {
	txnRepo := svcmocks.NewMockTransactionStore(t)
	ledger := svcmocks.NewMockLedgerRecorder(t)
	prov := providermocks.NewMockPaymentProvider(t)
	txRunner := &fakeTxRunner{}

	tenantID := int64(42)
	orderNumber := "ORD-001"
	txn := paidTxn(tenantID, orderNumber)

	txnRepo.EXPECT().GetByOrderNumberForUpdate(mock.Anything, tenantID, orderNumber).Return(txn, nil)
	txnRepo.EXPECT().Update(mock.Anything, mock.MatchedBy(func(t *domain.PaymentTransaction) bool {
		return t.Status == domain.StatusRefunding
	})).Return(nil)

	prov.EXPECT().CreateRefund(mock.Anything, mock.MatchedBy(func(r provider.CreateRefundRequest) bool {
		return r.ProviderPaymentID == "pay-abc" && r.Amount == 100000
	})).Return(&provider.Refund{ProviderRefundID: "rfn-001", Status: "approved"}, nil)

	ledger.EXPECT().RecordRefund(mock.Anything, mock.Anything, mock.Anything, int64(100000)).Return(nil)

	svc := newTestService(t, txnRepo, ledger, txRunner)
	result, err := svc.Refund(context.Background(), service.RefundRequest{
		TenantID:    tenantID,
		OrderNumber: orderNumber,
		Amount:      100000,
		Reason:      "customer request",
	}, map[string]provider.PaymentProvider{"durianpay": prov})

	require.NoError(t, err)
	assert.Equal(t, domain.StatusRefunding, result.Status)
}

func TestPaymentService_Refund_AlreadyRefunded(t *testing.T) {
	txnRepo := svcmocks.NewMockTransactionStore(t)
	ledger := svcmocks.NewMockLedgerRecorder(t)
	txRunner := &fakeTxRunner{}

	tenantID := int64(42)
	orderNumber := "ORD-002"
	txn := paidTxn(tenantID, orderNumber)
	txn.Status = domain.StatusRefunded

	txnRepo.EXPECT().GetByOrderNumberForUpdate(mock.Anything, tenantID, orderNumber).Return(txn, nil)

	svc := newTestService(t, txnRepo, ledger, txRunner)
	result, err := svc.Refund(context.Background(), service.RefundRequest{
		TenantID:    tenantID,
		OrderNumber: orderNumber,
		Amount:      100000,
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, domain.StatusRefunded, result.Status)
}

func TestPaymentService_Refund_InvalidStatus(t *testing.T) {
	txnRepo := svcmocks.NewMockTransactionStore(t)
	ledger := svcmocks.NewMockLedgerRecorder(t)
	txRunner := &fakeTxRunner{}

	tenantID := int64(42)
	orderNumber := "ORD-003"
	txn := paidTxn(tenantID, orderNumber)
	txn.Status = domain.StatusPending // can't refund from pending

	txnRepo.EXPECT().GetByOrderNumberForUpdate(mock.Anything, tenantID, orderNumber).Return(txn, nil)

	svc := newTestService(t, txnRepo, ledger, txRunner)
	_, err := svc.Refund(context.Background(), service.RefundRequest{
		TenantID:    tenantID,
		OrderNumber: orderNumber,
		Amount:      100000,
	}, nil)

	require.Error(t, err)
	var inv domain.ErrInvalidStatusTransition
	assert.True(t, errors.As(err, &inv))
}

func TestPaymentService_Refund_OrderNotFound(t *testing.T) {
	txnRepo := svcmocks.NewMockTransactionStore(t)
	ledger := svcmocks.NewMockLedgerRecorder(t)
	txRunner := &fakeTxRunner{}

	notFound := domain.ErrNotFound{Entity: "payment_transaction", ID: "ORD-999"}
	txnRepo.EXPECT().GetByOrderNumberForUpdate(mock.Anything, int64(1), "ORD-999").Return(nil, notFound)

	svc := newTestService(t, txnRepo, ledger, txRunner)
	_, err := svc.Refund(context.Background(), service.RefundRequest{
		TenantID:    1,
		OrderNumber: "ORD-999",
		Amount:      100000,
	}, nil)

	require.Error(t, err)
	var nf domain.ErrNotFound
	assert.True(t, errors.As(err, &nf))
}

func TestPaymentService_Refund_UnknownProvider(t *testing.T) {
	txnRepo := svcmocks.NewMockTransactionStore(t)
	ledger := svcmocks.NewMockLedgerRecorder(t)
	txRunner := &fakeTxRunner{}

	tenantID := int64(42)
	orderNumber := "ORD-004"
	txn := paidTxn(tenantID, orderNumber)

	txnRepo.EXPECT().GetByOrderNumberForUpdate(mock.Anything, tenantID, orderNumber).Return(txn, nil)
	txnRepo.EXPECT().Update(mock.Anything, mock.Anything).Return(nil)

	svc := newTestService(t, txnRepo, ledger, txRunner)
	_, err := svc.Refund(context.Background(), service.RefundRequest{
		TenantID:    tenantID,
		OrderNumber: orderNumber,
		Amount:      100000,
	}, map[string]provider.PaymentProvider{}) // no "durianpay" entry

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no provider configured")
}

func TestPaymentService_Refund_ProviderError(t *testing.T) {
	txnRepo := svcmocks.NewMockTransactionStore(t)
	ledger := svcmocks.NewMockLedgerRecorder(t)
	prov := providermocks.NewMockPaymentProvider(t)
	txRunner := &fakeTxRunner{}

	tenantID := int64(42)
	orderNumber := "ORD-005"
	txn := paidTxn(tenantID, orderNumber)

	txnRepo.EXPECT().GetByOrderNumberForUpdate(mock.Anything, tenantID, orderNumber).Return(txn, nil)
	txnRepo.EXPECT().Update(mock.Anything, mock.Anything).Return(nil)
	prov.EXPECT().CreateRefund(mock.Anything, mock.Anything).Return(nil, errors.New("provider: 403 amount exceeds original"))

	svc := newTestService(t, txnRepo, ledger, txRunner)
	_, err := svc.Refund(context.Background(), service.RefundRequest{
		TenantID:    tenantID,
		OrderNumber: orderNumber,
		Amount:      999999,
	}, map[string]provider.PaymentProvider{"durianpay": prov})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider")
}
