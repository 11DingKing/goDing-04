package voyage

import (
	"errors"
	"fmt"
	"time"
)

// CancellationNotice is the latest time before sailing a booking can be
// cancelled without forfeiting the deposit.
const CancellationNotice = 72 * time.Hour

// Voyage represents a scheduled sailing on the China-Europe Arctic fast route.
type Voyage struct {
	ID                string    `json:"id"`
	VesselName        string    `json:"vessel_name"`
	CarrierID         string    `json:"carrier_id"`
	CarrierName       string    `json:"carrier_name"`
	OriginPort        string    `json:"origin_port"`
	DestinationPort   string    `json:"destination_port"`
	SailingTime       time.Time `json:"sailing_time"`
	ETA               time.Time `json:"eta"`
	TotalReeferSlots  int       `json:"total_reefer_slots"`
	BookedReeferSlots int       `json:"booked_reefer_slots"`
	CreatedAt         time.Time `json:"created_at"`
}

// New creates a voyage after validating required fields and schedule sanity.
func New(id, vessel, carrierID, carrierName, origin, dest string, sailing, eta time.Time, reeferSlots int) (*Voyage, error) {
	if id == "" || vessel == "" || origin == "" || dest == "" {
		return nil, errors.New("voyage: id, vessel, origin and destination are required")
	}
	if reeferSlots <= 0 {
		return nil, errors.New("voyage: reefer slots must be positive")
	}
	if !eta.After(sailing) {
		return nil, errors.New("voyage: eta must be after sailing time")
	}
	return &Voyage{
		ID:               id,
		VesselName:       vessel,
		CarrierID:        carrierID,
		CarrierName:      carrierName,
		OriginPort:       origin,
		DestinationPort:  dest,
		SailingTime:      sailing,
		ETA:              eta,
		TotalReeferSlots: reeferSlots,
		CreatedAt:        time.Now().UTC(),
	}, nil
}

// ReeferSlotsAvailable returns the number of unallocated reefer slots.
func (v *Voyage) ReeferSlotsAvailable() int {
	return v.TotalReeferSlots - v.BookedReeferSlots
}

// ReserveReefer allocates n reefer slots, failing if there is not enough capacity.
func (v *Voyage) ReserveReefer(n int) error {
	if n <= 0 {
		return errors.New("voyage: must reserve at least one reefer slot")
	}
	if v.BookedReeferSlots+n > v.TotalReeferSlots {
		return fmt.Errorf("voyage: insufficient reefer slots: available %d, requested %d", v.ReeferSlotsAvailable(), n)
	}
	v.BookedReeferSlots += n
	return nil
}

// ReleaseReefer returns n reefer slots to the pool (e.g. on cancellation).
func (v *Voyage) ReleaseReefer(n int) {
	v.BookedReeferSlots -= n
	if v.BookedReeferSlots < 0 {
		v.BookedReeferSlots = 0
	}
}

// CancellationDeadline returns the latest time a booking may cancel with a refund.
func (v *Voyage) CancellationDeadline() time.Time {
	return v.SailingTime.Add(-CancellationNotice)
}

// CanCancel reports whether a booking on this voyage may still cancel freely.
func (v *Voyage) CanCancel(now time.Time) bool {
	return !now.After(v.CancellationDeadline())
}
