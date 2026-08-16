package app_test

import (
	"testing"
	"time"

	"arcticfreight/internal/app"
	"arcticfreight/internal/domain/booking"
)

// voyageAt creates a voyage sailing at the given instant.
func voyageAt(t *testing.T, svc *app.Service, sailing time.Time, slots int) string {
	t.Helper()
	v, err := svc.CreateVoyage(app.CreateVoyageRequest{
		VesselName: "MV Cancel", CarrierID: "haijie", CarrierName: "Haijie Shipping",
		OriginPort: "Ningbo-Zhoushan", DestinationPort: "Hamburg",
		SailingTime: sailing, ETA: sailing.Add(19 * 24 * time.Hour), ReeferSlots: slots,
	})
	if err != nil {
		t.Fatalf("create voyage: %v", err)
	}
	return v.ID
}

func lockSlots(t *testing.T, svc *app.Service, vid, requestID string, containers int, paymentTime time.Time) string {
	t.Helper()
	b, err := svc.LockBooking(app.LockBookingRequest{
		RequestID: requestID, VoyageID: vid, ShipperID: "s-" + requestID, ShipperName: "Shipper " + requestID,
		CargoType: booking.CargoPowerBattery, ContainerCount: containers,
		PaymentTime: paymentTime, DepositCents: 100000,
	})
	if err != nil {
		t.Fatalf("lock %s: %v", requestID, err)
	}
	return b.ID
}

// TestLateCancellationReleasesCapacity asserts that a cancellation inside the
// 72 h window forfeits the deposit but still frees the reefer slots.
func TestLateCancellationReleasesCapacity(t *testing.T) {
	svc := newTestService(t)
	sailing := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	vid := voyageAt(t, svc, sailing, 4)
	bid := lockSlots(t, svc, vid, "late", 3, sailing.Add(-30*24*time.Hour))

	v, _ := svc.GetVoyage(vid)
	if v.ReeferSlotsAvailable() != 1 {
		t.Fatalf("expected 1 slot left after locking 3 of 4, got %d", v.ReeferSlotsAvailable())
	}

	// 24 h before sailing: inside the 72 h window.
	cancelled, err := svc.CancelBooking(bid, sailing.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != booking.StatusCancelled {
		t.Fatalf("expected cancelled, got %s", cancelled.Status)
	}
	if !cancelled.DepositForfeited || cancelled.DepositRefunded {
		t.Fatalf("expected the deposit forfeited for a late cancellation, got forfeited=%v refunded=%v",
			cancelled.DepositForfeited, cancelled.DepositRefunded)
	}

	v, _ = svc.GetVoyage(vid)
	if v.BookedReeferSlots != 0 {
		t.Fatalf("expected 0 booked slots after cancelling the only booking, got %d", v.BookedReeferSlots)
	}
	if v.ReeferSlotsAvailable() != 4 {
		t.Fatalf("expected all 4 slots available again, got %d", v.ReeferSlotsAvailable())
	}
}

// TestCapacityResellableAfterLateCancellation asserts the freed capacity can
// actually be locked again by another shipper.
func TestCapacityResellableAfterLateCancellation(t *testing.T) {
	svc := newTestService(t)
	sailing := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	vid := voyageAt(t, svc, sailing, 2)
	bid := lockSlots(t, svc, vid, "first", 2, sailing.Add(-30*24*time.Hour))

	if _, err := svc.CancelBooking(bid, sailing.Add(-10*time.Hour)); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if _, err := svc.LockBooking(app.LockBookingRequest{
		RequestID: "second", VoyageID: vid, ShipperID: "s2", ShipperName: "Waiting Shipper",
		CargoType: booking.CargoEnergyStorageCabinet, ContainerCount: 2,
		PaymentTime: sailing.Add(-9 * time.Hour), DepositCents: 100000,
	}); err != nil {
		t.Fatalf("expected the freed capacity to be lockable again: %v", err)
	}
	v, _ := svc.GetVoyage(vid)
	if v.BookedReeferSlots != 2 {
		t.Fatalf("expected 2 booked slots, got %d", v.BookedReeferSlots)
	}
}

// TestCancellationAtDeadlineInstantRefundsAndReleases pins the exact 72 h
// boundary: cancelling right on the deadline is still a free cancellation.
func TestCancellationAtDeadlineInstantRefundsAndReleases(t *testing.T) {
	svc := newTestService(t)
	sailing := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	vid := voyageAt(t, svc, sailing, 3)
	bid := lockSlots(t, svc, vid, "boundary", 2, sailing.Add(-30*24*time.Hour))

	cancelled, err := svc.CancelBooking(bid, sailing.Add(-72*time.Hour))
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !cancelled.DepositRefunded {
		t.Fatal("expected a refund exactly on the 72 h deadline")
	}
	v, _ := svc.GetVoyage(vid)
	if v.ReeferSlotsAvailable() != 3 {
		t.Fatalf("expected all 3 slots available again, got %d", v.ReeferSlotsAvailable())
	}
}

// TestEarlyCancellationReleasesCapacity keeps the already-working path covered.
func TestEarlyCancellationReleasesCapacity(t *testing.T) {
	svc := newTestService(t)
	sailing := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	vid := voyageAt(t, svc, sailing, 5)
	bid := lockSlots(t, svc, vid, "early", 2, sailing.Add(-40*24*time.Hour))

	cancelled, err := svc.CancelBooking(bid, sailing.Add(-20*24*time.Hour))
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !cancelled.DepositRefunded {
		t.Fatal("expected a refund for an early cancellation")
	}
	v, _ := svc.GetVoyage(vid)
	if v.ReeferSlotsAvailable() != 5 {
		t.Fatalf("expected all 5 slots available again, got %d", v.ReeferSlotsAvailable())
	}
}

// TestMixedCancellationsKeepCapacityConsistent asserts the booked-slot counter
// stays consistent with the surviving bookings across several cancellations.
func TestMixedCancellationsKeepCapacityConsistent(t *testing.T) {
	svc := newTestService(t)
	sailing := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	vid := voyageAt(t, svc, sailing, 10)

	early := lockSlots(t, svc, vid, "mix-early", 2, sailing.Add(-40*24*time.Hour))
	late := lockSlots(t, svc, vid, "mix-late", 3, sailing.Add(-39*24*time.Hour))
	keep := lockSlots(t, svc, vid, "mix-keep", 4, sailing.Add(-38*24*time.Hour))
	_ = keep

	if _, err := svc.CancelBooking(early, sailing.Add(-10*24*time.Hour)); err != nil {
		t.Fatalf("cancel early: %v", err)
	}
	if _, err := svc.CancelBooking(late, sailing.Add(-1*time.Hour)); err != nil {
		t.Fatalf("cancel late: %v", err)
	}

	v, _ := svc.GetVoyage(vid)
	if v.BookedReeferSlots != 4 {
		t.Fatalf("expected 4 booked slots (only the surviving booking), got %d", v.BookedReeferSlots)
	}
	if v.ReeferSlotsAvailable() != 6 {
		t.Fatalf("expected 6 slots available, got %d", v.ReeferSlotsAvailable())
	}
}
