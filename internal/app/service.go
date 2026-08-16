package app

import (
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	"arcticfreight/internal/domain/booking"
	"arcticfreight/internal/domain/handover"
	"arcticfreight/internal/domain/temperature"
	"arcticfreight/internal/domain/voyage"
	"arcticfreight/internal/store"
)

// Service orchestrates the four workflows: shipper lock, carrier confirm,
// port loading/transfer, and temperature reporting. It enforces the business
// rules around concurrency, idempotency and failure recovery.
type Service struct {
	store     *store.Store
	idCounter atomic.Int64
}

// New creates a Service backed by the given store.
func New(s *store.Store) *Service {
	return &Service{store: s}
}

func (s *Service) nextID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, s.idCounter.Add(1))
}

// --- Voyage ---

// CreateVoyageRequest is the input for creating a voyage.
type CreateVoyageRequest struct {
	VesselName      string
	CarrierID       string
	CarrierName     string
	OriginPort      string
	DestinationPort string
	SailingTime     time.Time
	ETA             time.Time
	ReeferSlots     int
}

func (s *Service) CreateVoyage(req CreateVoyageRequest) (*voyage.Voyage, error) {
	s.store.Lock()
	defer s.store.Unlock()
	v, err := voyage.New(
		s.nextID("voyage"), req.VesselName, req.CarrierID, req.CarrierName,
		req.OriginPort, req.DestinationPort, req.SailingTime, req.ETA, req.ReeferSlots,
	)
	if err != nil {
		return nil, err
	}
	s.store.SaveVoyage(v)
	s.store.Save()
	return v, nil
}

func (s *Service) GetVoyage(id string) (*voyage.Voyage, error) {
	s.store.RLock()
	defer s.store.RUnlock()
	v, ok := s.store.GetVoyage(id)
	if !ok {
		return nil, fmt.Errorf("voyage %s not found", id)
	}
	return v, nil
}

func (s *Service) ListVoyages() []*voyage.Voyage {
	s.store.RLock()
	defer s.store.RUnlock()
	return s.store.ListVoyages()
}

// --- Booking lock (concurrency boundary) ---

// LockBookingRequest is the input for a shipper locking reefer capacity.
type LockBookingRequest struct {
	RequestID      string
	VoyageID       string
	ShipperID      string
	ShipperName    string
	CargoType      booking.CargoType
	ContainerCount int
	PaymentTime    time.Time
	DepositCents   int64
}

// LockResult holds the outcome for a single lock request in a batch.
type LockResult struct {
	Booking *booking.Booking
	Err     error
}

// LockBooking locks a single booking. It is a convenience wrapper around
// LockBookings.
func (s *Service) LockBooking(req LockBookingRequest) (*booking.Booking, error) {
	results, err := s.LockBookings([]LockBookingRequest{req})
	if err != nil {
		return nil, err
	}
	if results[0].Err != nil {
		return nil, results[0].Err
	}
	return results[0].Booking, nil
}

// LockBookings processes a batch of lock requests sorted by payment time,
// guaranteeing payment-time ordering and preventing overbooking. Requests are
// idempotent by RequestID.
func (s *Service) LockBookings(reqs []LockBookingRequest) ([]LockResult, error) {
	s.store.Lock()
	defer s.store.Unlock()

	// Stable sort by payment time to enforce the "first paid, first served" rule.
	indices := make([]int, len(reqs))
	for i := range reqs {
		indices[i] = i
	}
	sort.SliceStable(indices, func(a, b int) bool {
		return reqs[indices[a]].PaymentTime.Before(reqs[indices[b]].PaymentTime)
	})

	results := make([]LockResult, len(reqs))
	for _, idx := range indices {
		req := reqs[idx]

		// Idempotency: a repeated request returns the existing booking.
		if req.RequestID != "" {
			if existing, ok := s.findBookingByRequest(req.RequestID); ok {
				results[idx] = LockResult{Booking: existing}
				continue
			}
		}

		v, ok := s.store.GetVoyage(req.VoyageID)
		if !ok {
			results[idx] = LockResult{Err: fmt.Errorf("voyage %s not found", req.VoyageID)}
			continue
		}
		if err := v.ReserveReefer(req.ContainerCount); err != nil {
			results[idx] = LockResult{Err: err}
			continue
		}
		b, err := booking.New(
			s.nextID("booking"), req.RequestID, req.VoyageID, req.ShipperID, req.ShipperName,
			req.CargoType, req.ContainerCount, req.PaymentTime, req.DepositCents,
		)
		if err != nil {
			v.ReleaseReefer(req.ContainerCount)
			results[idx] = LockResult{Err: err}
			continue
		}
		s.store.SaveBooking(b)
		s.store.GetOrCreateTempLog(b.ID)
		results[idx] = LockResult{Booking: b}
	}
	s.store.Save()
	return results, nil
}

func (s *Service) findBookingByRequest(requestID string) (*booking.Booking, bool) {
	for _, b := range s.store.ListBookings() {
		if b.RequestID == requestID {
			return b, true
		}
	}
	return nil, false
}

func (s *Service) GetBooking(id string) (*booking.Booking, error) {
	s.store.RLock()
	defer s.store.RUnlock()
	b, ok := s.store.GetBooking(id)
	if !ok {
		return nil, fmt.Errorf("booking %s not found", id)
	}
	return b, nil
}

// --- Carrier confirm + reefer power allocation ---

func (s *Service) ConfirmBooking(bookingID, powerCircuitID string) (*booking.Booking, error) {
	s.store.Lock()
	defer s.store.Unlock()
	b, ok := s.store.GetBooking(bookingID)
	if !ok {
		return nil, fmt.Errorf("booking %s not found", bookingID)
	}
	if err := b.Confirm(powerCircuitID); err != nil {
		return nil, err
	}
	s.store.Save()
	return b, nil
}

// --- Cancellation ---

func (s *Service) CancelBooking(bookingID string, now time.Time) (*booking.Booking, error) {
	s.store.Lock()
	defer s.store.Unlock()
	b, ok := s.store.GetBooking(bookingID)
	if !ok {
		return nil, fmt.Errorf("booking %s not found", bookingID)
	}
	v, ok := s.store.GetVoyage(b.VoyageID)
	if !ok {
		return nil, fmt.Errorf("voyage %s not found", b.VoyageID)
	}
	if err := b.Cancel(v.CancellationDeadline(), now); err != nil {
		return nil, err
	}
	v.ReleaseReefer(b.ContainerCount)
	s.store.Save()
	return b, nil
}

// --- Port loading and transfer ---

func (s *Service) LoadCargo(bookingID string) error {
	s.store.Lock()
	defer s.store.Unlock()
	b, ok := s.store.GetBooking(bookingID)
	if !ok {
		return fmt.Errorf("booking %s not found", bookingID)
	}
	if err := b.Load(); err != nil {
		return err
	}
	s.store.Save()
	return nil
}

func (s *Service) StartTransit(bookingID string) error {
	s.store.Lock()
	defer s.store.Unlock()
	b, ok := s.store.GetBooking(bookingID)
	if !ok {
		return fmt.Errorf("booking %s not found", bookingID)
	}
	if err := b.StartTransit(); err != nil {
		return err
	}
	s.store.Save()
	return nil
}

func (s *Service) DischargeCargo(bookingID string, now time.Time) error {
	s.store.Lock()
	defer s.store.Unlock()
	b, ok := s.store.GetBooking(bookingID)
	if !ok {
		return fmt.Errorf("booking %s not found", bookingID)
	}
	if err := b.Discharge(); err != nil {
		return err
	}
	port := "Hamburg"
	if v, ok := s.store.GetVoyage(b.VoyageID); ok {
		port = v.DestinationPort
	}
	ho := handover.New(s.nextID("handover"), bookingID, port, now)
	s.store.SaveHandover(ho)
	s.store.Save()
	return nil
}

// --- Temperature handover ---

func (s *Service) CompleteHandover(bookingID string, now time.Time) (*handover.Handover, error) {
	s.store.Lock()
	defer s.store.Unlock()
	ho, ok := s.store.GetHandover(bookingID)
	if !ok {
		return nil, fmt.Errorf("handover for booking %s not found", bookingID)
	}
	if err := ho.Complete(now); err != nil {
		return nil, err
	}
	if b, ok := s.store.GetBooking(bookingID); ok {
		_ = b.CompleteHandover()
	}
	s.store.Save()
	return ho, nil
}

// --- Temperature recording ---

func (s *Service) RecordTemperature(bookingID string, seg temperature.Segment, ts time.Time, tempC float64) (*temperature.Reading, []temperature.Alert, error) {
	s.store.Lock()
	defer s.store.Unlock()
	b, ok := s.store.GetBooking(bookingID)
	if !ok {
		return nil, nil, fmt.Errorf("booking %s not found", bookingID)
	}
	if b.Status.IsTerminal() {
		return nil, nil, fmt.Errorf("cannot record temperature for booking in terminal status %q", b.Status)
	}
	log := s.store.GetOrCreateTempLog(bookingID)

	// Idempotency: a duplicate (segment, timestamp) returns the existing reading.
	for i := range log.Readings {
		if log.Readings[i].Segment == seg && log.Readings[i].Timestamp.Equal(ts) {
			alerts := log.CheckBreaches(time.Now().UTC())
			s.store.Save()
			return &log.Readings[i], alerts, nil
		}
	}

	reading := temperature.Reading{
		ID:        s.nextID("reading"),
		BookingID: bookingID,
		Segment:   seg,
		Timestamp: ts,
		TempC:     tempC,
		Source:    log.RecorderSource,
	}
	if err := log.RecordReading(reading); err != nil {
		return nil, nil, err
	}
	alerts := log.CheckBreaches(time.Now().UTC())
	s.store.Save()
	return &reading, alerts, nil
}

func (s *Service) GetTemperatureLog(bookingID string) (*temperature.Log, error) {
	s.store.RLock()
	defer s.store.RUnlock()
	log, ok := s.store.GetTempLog(bookingID)
	if !ok {
		return nil, fmt.Errorf("temperature log for booking %s not found", bookingID)
	}
	return log, nil
}

// --- Recorder failure recovery ---

func (s *Service) ReportRecorderDisconnect(bookingID string) error {
	s.store.Lock()
	defer s.store.Unlock()
	log := s.store.GetOrCreateTempLog(bookingID)
	log.SwitchToBackup()
	s.store.Save()
	return nil
}

func (s *Service) RecoverTemperatureData(bookingID string) (int, error) {
	s.store.Lock()
	defer s.store.Unlock()
	log, ok := s.store.GetTempLog(bookingID)
	if !ok {
		return 0, fmt.Errorf("temperature log for booking %s not found", bookingID)
	}
	filled := log.FillMissingWithAverage()
	s.store.Save()
	return filled, nil
}

// --- Sign-off and damage assessment ---

// SegmentDamage attributes a breach to a responsible party for a journey leg.
type SegmentDamage struct {
	Segment          temperature.Segment `json:"segment"`
	HadBreach        bool                `json:"had_breach"`
	BreachStart      time.Time           `json:"breach_start"`
	BreachEnd        time.Time           `json:"breach_end"`
	MinTemp          float64             `json:"min_temp"`
	MaxTemp          float64             `json:"max_temp"`
	ResponsibleParty string              `json:"responsible_party"`
}

// DamageReport summarises temperature breaches per segment for liability.
type DamageReport struct {
	BookingID string          `json:"booking_id"`
	HasDamage bool            `json:"has_damage"`
	Segments  []SegmentDamage `json:"segments"`
}

func responsibleParty(seg temperature.Segment) string {
	switch seg {
	case temperature.SegmentLoading:
		return "origin_port"
	case temperature.SegmentTransit:
		return "carrier"
	case temperature.SegmentDischarge:
		return "destination_port"
	case temperature.SegmentHandover:
		return "temp_service"
	default:
		return "unknown"
	}
}

func (s *Service) assessDamageUnlocked(bookingID string) *DamageReport {
	report := &DamageReport{BookingID: bookingID}
	if log, ok := s.store.GetTempLog(bookingID); ok {
		for _, alert := range log.Alerts {
			report.Segments = append(report.Segments, SegmentDamage{
				Segment:          alert.Segment,
				HadBreach:        true,
				BreachStart:      alert.BreachStart,
				BreachEnd:        alert.BreachEnd,
				MinTemp:          alert.MinTemp,
				MaxTemp:          alert.MaxTemp,
				ResponsibleParty: responsibleParty(alert.Segment),
			})
			report.HasDamage = true
		}
	}
	return report
}

func (s *Service) AssessDamage(bookingID string) (*DamageReport, error) {
	s.store.RLock()
	defer s.store.RUnlock()
	if _, ok := s.store.GetBooking(bookingID); !ok {
		return nil, fmt.Errorf("booking %s not found", bookingID)
	}
	return s.assessDamageUnlocked(bookingID), nil
}

func (s *Service) SignReceipt(bookingID string, now time.Time) (*DamageReport, error) {
	s.store.Lock()
	defer s.store.Unlock()
	b, ok := s.store.GetBooking(bookingID)
	if !ok {
		return nil, fmt.Errorf("booking %s not found", bookingID)
	}
	// Final breach sweep before signing.
	if log, ok := s.store.GetTempLog(bookingID); ok {
		log.CheckBreaches(now)
	}
	if err := b.Sign(); err != nil {
		return nil, err
	}
	report := s.assessDamageUnlocked(bookingID)
	s.store.Save()
	return report, nil
}

// --- Background-task hooks (called by the scheduler) ---

// CheckTemperatureBreaches sweeps every log and returns newly raised alerts.
func (s *Service) CheckTemperatureBreaches(now time.Time) []temperature.Alert {
	s.store.Lock()
	defer s.store.Unlock()
	var raised []temperature.Alert
	for _, log := range s.store.ListTempLogs() {
		raised = append(raised, log.CheckBreaches(now)...)
	}
	if len(raised) > 0 {
		s.store.Save()
	}
	return raised
}

// CheckHandoverTimeouts marks overdue handovers for manual intervention.
func (s *Service) CheckHandoverTimeouts(now time.Time) []*handover.Handover {
	s.store.Lock()
	defer s.store.Unlock()
	var overdue []*handover.Handover
	for _, h := range s.store.ListHandovers() {
		if h.IsOverdue(now) {
			h.MarkManualIntervention(now)
			overdue = append(overdue, h)
		}
	}
	if len(overdue) > 0 {
		s.store.Save()
	}
	return overdue
}
