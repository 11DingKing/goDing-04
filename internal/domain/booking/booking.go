package booking

import (
	"errors"
	"fmt"
	"time"
)

// Status enumerates the booking lifecycle states.
type Status string

const (
	StatusLocked     Status = "locked"
	StatusConfirmed  Status = "confirmed"
	StatusLoaded     Status = "loaded"
	StatusInTransit  Status = "in_transit"
	StatusDischarged Status = "discharged"
	StatusHandedOver Status = "handed_over"
	StatusSigned     Status = "signed"
	StatusCancelled  Status = "cancelled"
)

// CargoType identifies temperature-sensitive goods carried on the route.
type CargoType string

const (
	CargoPowerBattery         CargoType = "power_battery"
	CargoEnergyStorageCabinet CargoType = "energy_storage_cabinet"
)

// RequiresReefer reports whether the cargo type needs refrigerated power.
func (c CargoType) RequiresReefer() bool {
	return c == CargoPowerBattery || c == CargoEnergyStorageCabinet
}

// IsTerminal reports whether the booking has reached a final state.
func (s Status) IsTerminal() bool {
	return s == StatusSigned || s == StatusCancelled
}

// Booking is the aggregate root for a shipper's cargo reservation.
type Booking struct {
	ID               string     `json:"id"`
	RequestID        string     `json:"request_id"`
	VoyageID         string     `json:"voyage_id"`
	ShipperID        string     `json:"shipper_id"`
	ShipperName      string     `json:"shipper_name"`
	CargoType        CargoType  `json:"cargo_type"`
	ContainerCount   int        `json:"container_count"`
	ReeferNeeded     bool       `json:"reefer_needed"`
	Status           Status     `json:"status"`
	PaymentTime      time.Time  `json:"payment_time"`
	DepositCents     int64      `json:"deposit_cents"`
	DepositPaid      bool       `json:"deposit_paid"`
	DepositRefunded  bool       `json:"deposit_refunded"`
	DepositForfeited bool       `json:"deposit_forfeited"`
	PowerAllocated   bool       `json:"power_allocated"`
	PowerCircuitID   string     `json:"power_circuit_id"`
	CancelledAt      *time.Time `json:"cancelled_at,omitempty"`
	CancellationNote string     `json:"cancellation_note,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// New creates a booking in the Locked state after validating inputs.
func New(id, requestID, voyageID, shipperID, shipperName string, cargo CargoType, containers int, paymentTime time.Time, depositCents int64) (*Booking, error) {
	if id == "" || voyageID == "" || shipperID == "" {
		return nil, errors.New("booking: missing required fields")
	}
	if containers <= 0 {
		return nil, errors.New("booking: container count must be positive")
	}
	if !cargo.RequiresReefer() {
		return nil, fmt.Errorf("booking: cargo type %q is not supported (reefer required)", cargo)
	}
	now := time.Now().UTC()
	return &Booking{
		ID:             id,
		RequestID:      requestID,
		VoyageID:       voyageID,
		ShipperID:      shipperID,
		ShipperName:    shipperName,
		CargoType:      cargo,
		ContainerCount: containers,
		ReeferNeeded:   true,
		Status:         StatusLocked,
		PaymentTime:    paymentTime,
		DepositCents:   depositCents,
		DepositPaid:    true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

// Confirm transitions the booking to Confirmed and allocates reefer power.
func (b *Booking) Confirm(powerCircuitID string) error {
	if b.Status != StatusLocked {
		return fmt.Errorf("booking: cannot confirm from status %q", b.Status)
	}
	if powerCircuitID == "" {
		return errors.New("booking: power circuit id required to confirm")
	}
	b.Status = StatusConfirmed
	b.PowerAllocated = true
	b.PowerCircuitID = powerCircuitID
	b.touch()
	return nil
}

// Load transitions the booking to Loaded after port loading.
func (b *Booking) Load() error {
	if b.Status != StatusConfirmed {
		return fmt.Errorf("booking: cannot load from status %q", b.Status)
	}
	b.Status = StatusLoaded
	b.touch()
	return nil
}

// StartTransit transitions the booking to InTransit.
func (b *Booking) StartTransit() error {
	if b.Status != StatusLoaded {
		return fmt.Errorf("booking: cannot start transit from status %q", b.Status)
	}
	b.Status = StatusInTransit
	b.touch()
	return nil
}

// Discharge transitions the booking to Discharged at the destination port.
func (b *Booking) Discharge() error {
	if b.Status != StatusInTransit {
		return fmt.Errorf("booking: cannot discharge from status %q", b.Status)
	}
	b.Status = StatusDischarged
	b.touch()
	return nil
}

// CompleteHandover transitions the booking to HandedOver after temp handover.
func (b *Booking) CompleteHandover() error {
	if b.Status != StatusDischarged {
		return fmt.Errorf("booking: cannot complete handover from status %q", b.Status)
	}
	b.Status = StatusHandedOver
	b.touch()
	return nil
}

// Sign transitions the booking to Signed (final delivery confirmation).
func (b *Booking) Sign() error {
	if b.Status != StatusHandedOver {
		return fmt.Errorf("booking: cannot sign from status %q", b.Status)
	}
	b.Status = StatusSigned
	b.touch()
	return nil
}

// Cancel transitions the booking to Cancelled. If now is on or before the
// voyage cancellation deadline the deposit is refunded; otherwise it is
// forfeited.
func (b *Booking) Cancel(deadline, now time.Time) error {
	if b.Status != StatusLocked && b.Status != StatusConfirmed {
		return fmt.Errorf("booking: cannot cancel from status %q", b.Status)
	}
	b.Status = StatusCancelled
	t := now
	b.CancelledAt = &t
	if !now.After(deadline) {
		b.DepositRefunded = true
		b.CancellationNote = "cancelled before 72h deadline; deposit refunded"
	} else {
		b.DepositForfeited = true
		b.CancellationNote = "cancelled within 72h of sailing; deposit forfeited"
	}
	b.touch()
	return nil
}

func (b *Booking) touch() {
	b.UpdatedAt = time.Now().UTC()
}
