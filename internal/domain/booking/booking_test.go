package booking_test

import (
	"testing"
	"time"

	"arcticfreight/internal/domain/booking"
)

func TestBookingNewValidation(t *testing.T) {
	// missing id
	if _, err := booking.New("", "r1", "v1", "s1", "Shipper", booking.CargoPowerBattery, 2, time.Now(), 100000); err == nil {
		t.Fatal("expected error for empty id")
	}
	// zero containers
	if _, err := booking.New("b1", "r1", "v1", "s1", "Shipper", booking.CargoPowerBattery, 0, time.Now(), 100000); err == nil {
		t.Fatal("expected error for zero containers")
	}
	// unsupported cargo type
	if _, err := booking.New("b1", "r1", "v1", "s1", "Shipper", "general_goods", 2, time.Now(), 100000); err == nil {
		t.Fatal("expected error for non-reefer cargo")
	}
	// valid
	b, err := booking.New("b1", "r1", "v1", "s1", "Shipper", booking.CargoPowerBattery, 2, time.Now(), 100000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Status != booking.StatusLocked {
		t.Fatalf("expected locked status, got %s", b.Status)
	}
	if !b.ReeferNeeded || !b.DepositPaid {
		t.Fatal("expected reefer needed and deposit paid")
	}
}

func TestBookingStateMachine(t *testing.T) {
	b, _ := booking.New("b1", "r1", "v1", "s1", "Shipper", booking.CargoEnergyStorageCabinet, 1, time.Now(), 50000)

	// invalid: load before confirm
	if err := b.Load(); err == nil {
		t.Fatal("expected error loading before confirm")
	}
	// confirm allocates power
	if err := b.Confirm("circuit-A1"); err != nil {
		t.Fatalf("confirm failed: %v", err)
	}
	if b.Status != booking.StatusConfirmed || !b.PowerAllocated || b.PowerCircuitID != "circuit-A1" {
		t.Fatalf("unexpected state after confirm: %+v", b)
	}
	// full happy path
	if err := b.Load(); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if err := b.StartTransit(); err != nil {
		t.Fatalf("transit failed: %v", err)
	}
	if err := b.Discharge(); err != nil {
		t.Fatalf("discharge failed: %v", err)
	}
	if err := b.CompleteHandover(); err != nil {
		t.Fatalf("handover failed: %v", err)
	}
	if err := b.Sign(); err != nil {
		t.Fatalf("sign failed: %v", err)
	}
	if b.Status != booking.StatusSigned {
		t.Fatalf("expected signed, got %s", b.Status)
	}
	// cannot transition from terminal
	if err := b.Load(); err == nil {
		t.Fatal("expected error loading from signed state")
	}
}

func TestBookingCancelBeforeDeadline(t *testing.T) {
	b, _ := booking.New("b1", "r1", "v1", "s1", "Shipper", booking.CargoPowerBattery, 1, time.Now(), 80000)
	sailing := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	deadline := sailing.Add(-72 * time.Hour) // 2026-11-28
	now := deadline.Add(-1 * time.Hour)      // 2026-11-27, before deadline

	if err := b.Cancel(deadline, now); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if b.Status != booking.StatusCancelled {
		t.Fatalf("expected cancelled, got %s", b.Status)
	}
	if !b.DepositRefunded {
		t.Fatal("expected deposit refunded for early cancel")
	}
	if b.DepositForfeited {
		t.Fatal("expected deposit not forfeited")
	}
}

func TestBookingCancelAfterDeadline(t *testing.T) {
	b, _ := booking.New("b1", "r1", "v1", "s1", "Shipper", booking.CargoPowerBattery, 1, time.Now(), 80000)
	sailing := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	deadline := sailing.Add(-72 * time.Hour)
	now := deadline.Add(1 * time.Hour) // within 72h of sailing

	if err := b.Cancel(deadline, now); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if !b.DepositForfeited {
		t.Fatal("expected deposit forfeited for late cancel")
	}
	if b.DepositRefunded {
		t.Fatal("expected deposit not refunded")
	}
	// cannot cancel a terminal booking again
	if err := b.Cancel(deadline, now); err == nil {
		t.Fatal("expected error cancelling already-cancelled booking")
	}
}
