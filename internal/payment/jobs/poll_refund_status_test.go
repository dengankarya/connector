package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/dengankarya/connector/internal/payment/jobs"
	jobmocks "github.com/dengankarya/connector/internal/payment/jobs/mocks"
	"github.com/dengankarya/connector/internal/payment/provider"
	"github.com/dengankarya/connector/internal/worker"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func discardLogger() *logrus.Logger {
	l := logrus.New()
	l.SetLevel(logrus.PanicLevel)
	return l
}

func makeTask(txnID uuid.UUID, paymentID string) *asynq.Task {
	payload, _ := json.Marshal(jobs.PollRefundStatusPayload{
		TransactionID:     txnID.String(),
		ProviderPaymentID: paymentID,
		TenantID:          1,
		RefundAmount:      100000,
	})
	return asynq.NewTask(worker.TaskPollRefundStatus, payload)
}

func TestPollRefundStatusJob_RefundNotYetFound(t *testing.T) {
	txnID := uuid.New()
	checker := jobmocks.NewMockRefundStatusChecker(t)

	checker.EXPECT().GetRefundsByPaymentID(mock.Anything, "pay-abc").Return([]provider.Refund{}, nil)

	job := jobs.NewPollRefundStatusJob(checker, nil, nil, nil, discardLogger())
	err := job.ProcessTask(context.Background(), makeTask(txnID, "pay-abc"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found yet")
}

func TestPollRefundStatusJob_RefundNotDone(t *testing.T) {
	txnID := uuid.New()
	checker := jobmocks.NewMockRefundStatusChecker(t)

	checker.EXPECT().GetRefundsByPaymentID(mock.Anything, "pay-abc").Return([]provider.Refund{
		{ExternalID: "refund:" + txnID.String(), Status: "approved"}, // not "done" yet
	}, nil)

	job := jobs.NewPollRefundStatusJob(checker, nil, nil, nil, discardLogger())
	err := job.ProcessTask(context.Background(), makeTask(txnID, "pay-abc"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not done yet")
}

func TestPollRefundStatusJob_CheckerError(t *testing.T) {
	txnID := uuid.New()
	checker := jobmocks.NewMockRefundStatusChecker(t)

	checker.EXPECT().GetRefundsByPaymentID(mock.Anything, "pay-abc").Return(nil, errors.New("network timeout"))

	job := jobs.NewPollRefundStatusJob(checker, nil, nil, nil, discardLogger())
	err := job.ProcessTask(context.Background(), makeTask(txnID, "pay-abc"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "network timeout")
}

func TestPollRefundStatusJob_InvalidPayload(t *testing.T) {
	checker := jobmocks.NewMockRefundStatusChecker(t)
	job := jobs.NewPollRefundStatusJob(checker, nil, nil, nil, discardLogger())

	task := asynq.NewTask(worker.TaskPollRefundStatus, []byte("not-valid-json"))
	err := job.ProcessTask(context.Background(), task)

	require.Error(t, err)
	assert.True(t, errors.Is(err, asynq.SkipRetry))
}

func TestPollRefundStatusJob_NilChecker(t *testing.T) {
	txnID := uuid.New()
	job := jobs.NewPollRefundStatusJob(nil, nil, nil, nil, discardLogger())

	err := job.ProcessTask(context.Background(), makeTask(txnID, "pay-abc"))
	require.NoError(t, err) // nil checker is a no-op
}

func TestNewPollRefundStatusTask(t *testing.T) {
	txnID := uuid.New()
	task, opts := jobs.NewPollRefundStatusTask(txnID, "pay-abc", 1, 150000)

	assert.Equal(t, worker.TaskPollRefundStatus, task.Type())
	assert.NotEmpty(t, opts)

	var p jobs.PollRefundStatusPayload
	require.NoError(t, json.Unmarshal(task.Payload(), &p))
	assert.Equal(t, txnID.String(), p.TransactionID)
	assert.Equal(t, "pay-abc", p.ProviderPaymentID)
	assert.Equal(t, int64(150000), p.RefundAmount)
}
