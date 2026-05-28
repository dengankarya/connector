package webhook

import (
	"context"
	"fmt"

	"github.com/dengankarya/connector/internal/payment/domain"
	"github.com/dengankarya/connector/internal/payment/repository"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

// ReplayService provides safe, idempotent replay and retry of webhook events.
// All replays re-use the same Processor.Process() path — no special code paths.
type ReplayService struct {
	eventRepo *repository.WebhookEventRepository
	enqueuer  *asynq.Client
	logger    *logrus.Logger
}

// NewReplayService creates a ReplayService.
func NewReplayService(
	eventRepo *repository.WebhookEventRepository,
	enqueuer *asynq.Client,
	logger *logrus.Logger,
) *ReplayService {
	return &ReplayService{
		eventRepo: eventRepo,
		enqueuer:  enqueuer,
		logger:    logger,
	}
}

// ReplayEvent replays a single webhook event by ID.
//
// Safety rules:
//   - If the event is already "processed", returns ErrEventAlreadyProcessed unless force=true.
//   - If force=true the event status is reset and re-enqueued.
//   - The Processor.Process() path enforces full idempotency (SELECT FOR UPDATE + reference_id).
func (s *ReplayService) ReplayEvent(ctx context.Context, eventID uuid.UUID, force bool) error {
	log := s.logger.WithFields(logrus.Fields{
		"component":        "webhook_replay",
		"webhook_event_id": eventID,
		"force":            force,
	})

	event, err := s.eventRepo.GetByID(ctx, eventID)
	if err != nil {
		return fmt.Errorf("replay: get event: %w", err)
	}

	if event.ProcessingStatus == domain.WebhookStatusProcessed && !force {
		return domain.ErrEventAlreadyProcessed
	}

	// Reset the event so the processor will pick it up again.
	if err := s.eventRepo.ResetForReplay(ctx, eventID); err != nil {
		return fmt.Errorf("replay: reset event: %w", err)
	}

	task, opts := NewTask(eventID)
	if _, err := s.enqueuer.EnqueueContext(ctx, task, opts...); err != nil {
		return fmt.Errorf("replay: enqueue task: %w", err)
	}

	log.WithField("event_type", event.EventType).Warn("webhook event queued for replay")
	return nil
}

// ReplayFailed re-enqueues all failed events up to limit.
// Returns the count of events re-queued and any partial error.
func (s *ReplayService) ReplayFailed(ctx context.Context, limit int) (int, error) {
	events, err := s.eventRepo.ListFailed(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("replay_failed: list events: %w", err)
	}

	var count int
	for _, e := range events {
		if err := s.ReplayEvent(ctx, e.ID, false); err != nil {
			s.logger.WithFields(logrus.Fields{
				"webhook_event_id": e.ID,
				"error":            err.Error(),
			}).Error("replay_failed: failed to replay event")
			continue
		}
		count++
	}

	s.logger.WithFields(logrus.Fields{
		"requested": limit,
		"replayed":  count,
		"total":     len(events),
	}).Info("replay_failed: batch complete")
	return count, nil
}

// MarkDeadLettered moves a failed event to dead_lettered status after exhausting retries.
// This stops automatic retry and requires a manual force-replay to reprocess.
func (s *ReplayService) MarkDeadLettered(ctx context.Context, eventID uuid.UUID) error {
	if err := s.eventRepo.UpdateStatus(ctx, eventID, domain.WebhookStatusDeadLettered, "max retries exhausted"); err != nil {
		return fmt.Errorf("mark dead lettered: %w", err)
	}
	s.logger.WithField("webhook_event_id", eventID).Warn("webhook event moved to dead_lettered")
	return nil
}
