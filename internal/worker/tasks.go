package worker

// Task type names — must match between enqueuer (payment/controller) and handler (worker).
const TaskXenplatformAccountUpdated = "xenplatform:account_updated"

// XenplatformAccountUpdatedPayload is the task payload stored in Redis.
// It carries the raw Xendit webhook event so the handler can process it independently.
type XenplatformAccountUpdatedPayload struct {
	Event      string         `json:"event"`
	BusinessID string         `json:"business_id"`
	Data       map[string]any `json:"data"`
}
