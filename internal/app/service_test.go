package app_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"arcticfreight/internal/app"
	"arcticfreight/internal/domain/booking"
	"arcticfreight/internal/domain/handover"
	"arcticfreight/internal/domain/temperature"
	"arcticfreight/internal/store"
)

func newTestService(t *testing.T) *app.Service {
	t.Helper()
	return app.New(store.New(""))
}

func futureSailing() time.Time {
	return time.Now().Add(30 * 24 * time.Hour)
}

func createVoyage(t *testing.T, svc *app.Service, slots int) string {
	t.Helper()
	v, err := svc.CreateVoyage(app.CreateVoyageRequest{
		VesselName: "MV Test", CarrierID: "haijie", CarrierName: "Haijie Shipping",
		OriginPort: "Ningbo-Zhoushan", DestinationPort: "Hamburg",
		SailingTime: futureSailing(), ETA: futureSailing().Add(20 * 24 * time.Hour), ReeferSlots: slots,
	})
	if err != nil {
		t.Fatalf("create voyage: %v", err)
	}
	return v.ID
}

func TestCreateVoyageAndLockBooking(t *testing.T) {
	svc := newTestService(t)
	vid := createVoyage(t, svc, 5)

	b, err := svc.LockBooking(app.LockBookingRequest{
		RequestID: "req-1", VoyageID: vid, ShipperID: "shipper-1", ShipperName: "Ningbo Battery Co",
		CargoType: booking.CargoPowerBattery, ContainerCount: 2, PaymentTime: time.Now(), DepositCents: 100000,
	})
	if err != nil {
		t.Fatalf("lock booking failed: %v", err)
	}
	if b.Status != booking.StatusLocked {
		t.Fatalf("expected locked, got %s", b.Status)
	}
	v, _ := svc.GetVoyage(vid)
	if v.BookedReeferSlots != 2 {
		t.Fatalf("expected 2 booked slots, got %d", v.BookedReeferSlots)
	}
	// idempotency: same RequestID returns the same booking.
	b2, err := svc.LockBooking(app.LockBookingRequest{
		RequestID: "req-1", VoyageID: vid, ShipperID: "shipper-1", ShipperName: "Ningbo Battery Co",
		CargoType: booking.CargoPowerBattery, ContainerCount: 2, PaymentTime: time.Now(), DepositCents: 100000,
	})
	if err != nil {
		t.Fatalf("idempotent lock failed: %v", err)
	}
	if b2.ID != b.ID {
		t.Fatal("expected same booking id for idempotent request")
	}
	v, _ = svc.GetVoyage(vid)
	if v.BookedReeferSlots != 2 {
		t.Fatalf("expected 2 booked slots after idempotent lock, got %d", v.BookedReeferSlots)
	}
}

func TestLockBookingsPaymentTimeOrder(t *testing.T) {
	svc := newTestService(t)
	vid := createVoyage(t, svc, 1) // only one slot

	base := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	reqs := []app.LockBookingRequest{
		{RequestID: "late", VoyageID: vid, ShipperID: "s-late", ShipperName: "Late",
			CargoType: booking.CargoPowerBattery, ContainerCount: 1, PaymentTime: base.Add(2 * time.Hour), DepositCents: 50000},
		{RequestID: "early", VoyageID: vid, ShipperID: "s-early", ShipperName: "Early",
			CargoType: booking.CargoPowerBattery, ContainerCount: 1, PaymentTime: base, DepositCents: 50000},
		{RequestID: "mid", VoyageID: vid, ShipperID: "s-mid", ShipperName: "Mid",
			CargoType: booking.CargoPowerBattery, ContainerCount: 1, PaymentTime: base.Add(1 * time.Hour), DepositCents: 50000},
	}
	results, err := svc.LockBookings(reqs)
	if err != nil {
		t.Fatalf("lock bookings failed: %v", err)
	}

	successCount := 0
	for i, res := range results {
		if res.Err == nil {
			successCount++
			if reqs[i].RequestID != "early" {
				t.Fatalf("expected early to win, but %s succeeded", reqs[i].RequestID)
			}
		}
	}
	if successCount != 1 {
		t.Fatalf("expected exactly 1 successful booking, got %d", successCount)
	}
	v, _ := svc.GetVoyage(vid)
	if v.BookedReeferSlots > v.TotalReeferSlots {
		t.Fatalf("overbooking detected: %d > %d", v.BookedReeferSlots, v.TotalReeferSlots)
	}
}

func TestConcurrentLockNoOverbooking(t *testing.T) {
	svc := newTestService(t)
	vid := createVoyage(t, svc, 3)

	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.LockBooking(app.LockBookingRequest{
				RequestID: fmt.Sprintf("c-%d", idx), VoyageID: vid, ShipperID: "shipper", ShipperName: "Shipper",
				CargoType: booking.CargoPowerBattery, ContainerCount: 1,
				PaymentTime: time.Now(), DepositCents: 50000,
			})
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if success > 3 {
		t.Fatalf("overbooking: %d successful bookings for 3 slots", success)
	}
	if success != 3 {
		t.Fatalf("expected exactly 3 successful bookings, got %d", success)
	}
	v, _ := svc.GetVoyage(vid)
	if v.BookedReeferSlots != 3 {
		t.Fatalf("expected 3 booked slots, got %d", v.BookedReeferSlots)
	}
}

func TestCancelBookingEndToEnd(t *testing.T) {
	svc := newTestService(t)
	vid := createVoyage(t, svc, 5)

	b, err := svc.LockBooking(app.LockBookingRequest{
		RequestID: "cancel-1", VoyageID: vid, ShipperID: "s1", ShipperName: "Shipper",
		CargoType: booking.CargoPowerBattery, ContainerCount: 2, PaymentTime: time.Now(), DepositCents: 80000,
	})
	if err != nil {
		t.Fatalf("lock failed: %v", err)
	}
	// Cancel well before the 72h deadline (voyage sails in 30 days).
	b2, err := svc.CancelBooking(b.ID, time.Now())
	if err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if b2.Status != booking.StatusCancelled || !b2.DepositRefunded {
		t.Fatalf("expected cancelled + refunded, got %s refunded=%v", b2.Status, b2.DepositRefunded)
	}
	v, _ := svc.GetVoyage(vid)
	if v.BookedReeferSlots != 0 {
		t.Fatalf("expected 0 booked slots after cancel, got %d", v.BookedReeferSlots)
	}
}

func TestTemperatureBreachAndDamageAssessment(t *testing.T) {
	svc := newTestService(t)
	vid := createVoyage(t, svc, 5)

	b, _ := svc.LockBooking(app.LockBookingRequest{
		RequestID: "breach-1", VoyageID: vid, ShipperID: "s1", ShipperName: "Shipper",
		CargoType: booking.CargoEnergyStorageCabinet, ContainerCount: 1, PaymentTime: time.Now(), DepositCents: 100000,
	})
	svc.ConfirmBooking(b.ID, "circuit-B1")
	svc.LoadCargo(b.ID)
	svc.StartTransit(b.ID)

	base := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		_, alerts, err := svc.RecordTemperature(b.ID, temperature.SegmentTransit, base.Add(time.Duration(i)*10*time.Minute), 28.0)
		if err != nil {
			t.Fatalf("record temp %d failed: %v", i, err)
		}
		if i == 2 && len(alerts) != 1 {
			t.Fatalf("expected 1 alert on 3rd reading, got %d", len(alerts))
		}
	}

	svc.DischargeCargo(b.ID, base.Add(30*time.Minute))
	svc.CompleteHandover(b.ID, base.Add(31*time.Minute))
	report, err := svc.SignReceipt(b.ID, base.Add(40*time.Minute))
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}
	if !report.HasDamage {
		t.Fatal("expected damage report to show damage")
	}
	if len(report.Segments) != 1 {
		t.Fatalf("expected 1 damaged segment, got %d", len(report.Segments))
	}
	if report.Segments[0].ResponsibleParty != "carrier" {
		t.Fatalf("expected carrier responsible for transit breach, got %s", report.Segments[0].ResponsibleParty)
	}
}

func TestHandoverTimeoutManualIntervention(t *testing.T) {
	svc := newTestService(t)
	vid := createVoyage(t, svc, 5)

	b, _ := svc.LockBooking(app.LockBookingRequest{
		RequestID: "timeout-1", VoyageID: vid, ShipperID: "s1", ShipperName: "Shipper",
		CargoType: booking.CargoPowerBattery, ContainerCount: 1, PaymentTime: time.Now(), DepositCents: 100000,
	})
	svc.ConfirmBooking(b.ID, "circuit-C1")
	svc.LoadCargo(b.ID)
	svc.StartTransit(b.ID)

	discharge := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	svc.DischargeCargo(b.ID, discharge)

	// 3 hours after discharge exceeds the 2h handover deadline.
	overdue := svc.CheckHandoverTimeouts(discharge.Add(3 * time.Hour))
	if len(overdue) != 1 {
		t.Fatalf("expected 1 overdue handover, got %d", len(overdue))
	}
	if overdue[0].Status != handover.StatusManualIntervention {
		t.Fatalf("expected manual intervention, got %s", overdue[0].Status)
	}
	// handover can still be completed after manual resolution.
	ho, err := svc.CompleteHandover(b.ID, discharge.Add(3*time.Hour+5*time.Minute))
	if err != nil {
		t.Fatalf("complete after manual intervention failed: %v", err)
	}
	if ho.Status != handover.StatusCompleted {
		t.Fatalf("expected completed, got %s", ho.Status)
	}
}

func TestRecoverMissingTemperatureData(t *testing.T) {
	svc := newTestService(t)
	vid := createVoyage(t, svc, 5)

	b, _ := svc.LockBooking(app.LockBookingRequest{
		RequestID: "recover-1", VoyageID: vid, ShipperID: "s1", ShipperName: "Shipper",
		CargoType: booking.CargoPowerBattery, ContainerCount: 1, PaymentTime: time.Now(), DepositCents: 100000,
	})
	svc.ConfirmBooking(b.ID, "circuit-D1")

	base := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	svc.RecordTemperature(b.ID, temperature.SegmentTransit, base, 15.0)
	svc.RecordTemperature(b.ID, temperature.SegmentTransit, base.Add(10*time.Minute), 16.0)

	// Recorder loses power; backup resumes 30 min later (T=40).
	svc.ReportRecorderDisconnect(b.ID)
	svc.RecordTemperature(b.ID, temperature.SegmentTransit, base.Add(40*time.Minute), 18.0)

	filled, err := svc.RecoverTemperatureData(b.ID)
	if err != nil {
		t.Fatalf("recover failed: %v", err)
	}
	if filled != 2 {
		t.Fatalf("expected 2 filled readings (T=20, T=30), got %d", filled)
	}
	log, _ := svc.GetTemperatureLog(b.ID)
	if len(log.Readings) != 5 { // 2 + 1 + 2 filled
		t.Fatalf("expected 5 readings total, got %d", len(log.Readings))
	}
}

func TestSignReceiptNoDamage(t *testing.T) {
	svc := newTestService(t)
	vid := createVoyage(t, svc, 5)

	b, _ := svc.LockBooking(app.LockBookingRequest{
		RequestID: "sign-1", VoyageID: vid, ShipperID: "s1", ShipperName: "Shipper",
		CargoType: booking.CargoPowerBattery, ContainerCount: 1, PaymentTime: time.Now(), DepositCents: 100000,
	})
	svc.ConfirmBooking(b.ID, "circuit-E1")
	svc.LoadCargo(b.ID)
	svc.StartTransit(b.ID)

	base := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		svc.RecordTemperature(b.ID, temperature.SegmentTransit, base.Add(time.Duration(i)*10*time.Minute), 15.0)
	}
	svc.DischargeCargo(b.ID, base.Add(30*time.Minute))
	svc.CompleteHandover(b.ID, base.Add(31*time.Minute))

	report, err := svc.SignReceipt(b.ID, base.Add(40*time.Minute))
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}
	if report.HasDamage {
		t.Fatal("expected no damage for in-range temps")
	}
	b2, _ := svc.GetBooking(b.ID)
	if b2.Status != booking.StatusSigned {
		t.Fatalf("expected signed, got %s", b2.Status)
	}
}
