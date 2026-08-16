package handover

import (
	"fmt"
	"time"
)

// Status enumerates handover states at the destination port.
type Status string

const (
	StatusPending            Status = "pending"
	StatusCompleted          Status = "completed"
	StatusManualIntervention Status = "manual_intervention"
)

// HandoverTimeout is the maximum allowed time after discharge to complete the
// temperature-controlled handover at Hamburg.
const HandoverTimeout = 2 * time.Hour

// Handover tracks the temperature handover after a vessel discharges.
type Handover struct {
	ID            string     `json:"id"`
	BookingID     string     `json:"booking_id"`
	Port          string     `json:"port"`
	DischargeTime time.Time  `json:"discharge_time"`
	Deadline      time.Time  `json:"deadline"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	Status        Status     `json:"status"`
	ReceiverID    string     `json:"receiver_id,omitempty"`
}

// New creates a pending handover with the deadline derived from discharge time.
func New(id, bookingID, port string, dischargeTime time.Time) *Handover {
	return &Handover{
		ID:            id,
		BookingID:     bookingID,
		Port:          port,
		DischargeTime: dischargeTime,
		Deadline:      dischargeTime.Add(HandoverTimeout),
		Status:        StatusPending,
	}
}

// Complete finalises the handover. It may be called from pending or after
// manual intervention (once a human resolves the situation).
func (h *Handover) Complete(now time.Time) error {
	if h.Status != StatusPending && h.Status != StatusManualIntervention {
		return fmt.Errorf("handover: cannot complete from status %q", h.Status)
	}
	h.Status = StatusCompleted
	t := now
	h.CompletedAt = &t
	return nil
}

// MarkManualIntervention flags an overdue handover for human handling.
func (h *Handover) MarkManualIntervention(now time.Time) {
	if h.Status == StatusPending {
		h.Status = StatusManualIntervention
		t := now
		h.CompletedAt = &t
	}
}

// IsOverdue reports whether the handover is still pending past its deadline.
func (h *Handover) IsOverdue(now time.Time) bool {
	return h.Status == StatusPending && now.After(h.Deadline)
}
