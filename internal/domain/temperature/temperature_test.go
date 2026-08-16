package temperature_test

import (
	"testing"
	"time"

	"arcticfreight/internal/domain/temperature"
)

func TestTemperatureInRangeNoAlert(t *testing.T) {
	log := temperature.NewLog("b1")
	base := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		log.RecordReading(temperature.Reading{
			BookingID: "b1",
			Segment:   temperature.SegmentTransit,
			Timestamp: base.Add(time.Duration(i) * 10 * time.Minute),
			TempC:     15.0,
			Source:    temperature.SourcePrimary,
		})
	}
	alerts := log.CheckBreaches(base.Add(50 * time.Minute))
	if len(alerts) != 0 {
		t.Fatalf("expected no alerts for in-range temps, got %d", len(alerts))
	}
}

func TestTemperatureBreachAlert(t *testing.T) {
	log := temperature.NewLog("b1")
	base := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	// Three out-of-range readings spanning 20 minutes (>= 15 min window).
	for i, temp := range []float64{30.0, 31.0, 32.0} {
		log.RecordReading(temperature.Reading{
			BookingID: "b1",
			Segment:   temperature.SegmentTransit,
			Timestamp: base.Add(time.Duration(i) * 10 * time.Minute),
			TempC:     temp,
			Source:    temperature.SourcePrimary,
		})
	}
	alerts := log.CheckBreaches(base.Add(20 * time.Minute))
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	a := alerts[0]
	if a.Segment != temperature.SegmentTransit {
		t.Fatalf("expected transit segment, got %s", a.Segment)
	}
	if len(a.NotifiedParties) != 3 {
		t.Fatalf("expected 3 notified parties, got %d", len(a.NotifiedParties))
	}
	if a.MinTemp != 30.0 || a.MaxTemp != 32.0 {
		t.Fatalf("unexpected min/max temp: %f/%f", a.MinTemp, a.MaxTemp)
	}
	// idempotency: re-check must not duplicate the alert.
	if again := log.CheckBreaches(base.Add(21 * time.Minute)); len(again) != 0 {
		t.Fatalf("expected no new alerts on re-check, got %d", len(again))
	}
}

func TestTemperatureSwitchToBackup(t *testing.T) {
	log := temperature.NewLog("b1")
	if log.RecorderSource != temperature.SourcePrimary {
		t.Fatal("expected primary source initially")
	}
	log.SwitchToBackup()
	if log.RecorderSource != temperature.SourceBackup {
		t.Fatal("expected backup source after switch")
	}
	// new readings should adopt the active (backup) source.
	log.RecordReading(temperature.Reading{
		BookingID: "b1",
		Segment:   temperature.SegmentTransit,
		Timestamp: time.Now(),
		TempC:     10.0,
	})
	if log.Readings[0].Source != temperature.SourceBackup {
		t.Fatalf("expected reading source backup, got %s", log.Readings[0].Source)
	}
}

func TestTemperatureFillMissingAverage(t *testing.T) {
	log := temperature.NewLog("b1")
	log.SwitchToBackup()
	base := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	// Two readings 40 minutes apart: gap fills T=10, T=20, T=30.
	log.RecordReading(temperature.Reading{
		BookingID: "b1", Segment: temperature.SegmentTransit,
		Timestamp: base, TempC: 10.0, Source: temperature.SourceBackup,
	})
	log.RecordReading(temperature.Reading{
		BookingID: "b1", Segment: temperature.SegmentTransit,
		Timestamp: base.Add(40 * time.Minute), TempC: 20.0, Source: temperature.SourceBackup,
	})
	filled := log.FillMissingWithAverage()
	if filled != 3 {
		t.Fatalf("expected 3 filled readings, got %d", filled)
	}
	avg := (10.0 + 20.0) / 2
	filledCount := 0
	for _, r := range log.Readings {
		if r.Filled {
			filledCount++
			if r.TempC != avg {
				t.Fatalf("expected filled temp %f, got %f", avg, r.TempC)
			}
		}
	}
	if filledCount != 3 {
		t.Fatalf("expected 3 filled readings in log, got %d", filledCount)
	}
	// idempotency: second call fills nothing.
	if again := log.FillMissingWithAverage(); again != 0 {
		t.Fatalf("expected 0 filled on second call, got %d", again)
	}
}
