package temperature

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Segment identifies a leg of the journey for temperature attribution.
type Segment string

const (
	SegmentLoading   Segment = "loading"
	SegmentTransit   Segment = "transit"
	SegmentDischarge Segment = "discharge"
	SegmentHandover  Segment = "handover"
)

// Source identifies which recorder produced a reading.
type Source string

const (
	SourcePrimary Source = "primary"
	SourceBackup  Source = "backup"
)

// Enforced temperature thresholds for power batteries and storage cabinets.
const (
	MinTempC       = 0.0
	MaxTempC       = 25.0
	AlertWindow    = 15 * time.Minute
	RecordInterval = 10 * time.Minute
	RetentionDays  = 90
)

// InRange reports whether a temperature is within the allowed band.
func InRange(temp float64) bool {
	return temp >= MinTempC && temp <= MaxTempC
}

// Reading is a single temperature measurement for a booking.
type Reading struct {
	ID        string    `json:"id"`
	BookingID string    `json:"booking_id"`
	Segment   Segment   `json:"segment"`
	Timestamp time.Time `json:"timestamp"`
	TempC     float64   `json:"temp_c"`
	Source    Source    `json:"source"`
	Filled    bool      `json:"filled"`
}

// Alert is raised when a breach persists longer than AlertWindow.
type Alert struct {
	ID              string    `json:"id"`
	BookingID       string    `json:"booking_id"`
	Segment         Segment   `json:"segment"`
	TriggeredAt     time.Time `json:"triggered_at"`
	BreachStart     time.Time `json:"breach_start"`
	BreachEnd       time.Time `json:"breach_end"`
	MinTemp         float64   `json:"min_temp"`
	MaxTemp         float64   `json:"max_temp"`
	NotifiedParties []string  `json:"notified_parties"`
}

// Log holds the temperature record for a single booking.
type Log struct {
	BookingID      string    `json:"booking_id"`
	Readings       []Reading `json:"readings"`
	Alerts         []Alert   `json:"alerts"`
	RecorderSource Source    `json:"recorder_source"`
}

// NewLog creates an empty log using the primary recorder.
func NewLog(bookingID string) *Log {
	return &Log{
		BookingID:      bookingID,
		RecorderSource: SourcePrimary,
	}
}

// RecordReading appends a measurement, defaulting the source to the active recorder.
func (l *Log) RecordReading(r Reading) error {
	if r.BookingID != l.BookingID {
		return errors.New("temperature: reading booking id mismatch")
	}
	if r.Source == "" {
		r.Source = l.RecorderSource
	}
	l.Readings = append(l.Readings, r)
	return nil
}

// SwitchToBackup marks the active recorder as the backup unit.
func (l *Log) SwitchToBackup() {
	l.RecorderSource = SourceBackup
}

// CheckBreaches scans readings for sustained out-of-range runs and records new
// alerts. It is idempotent: alerts already recorded are not duplicated.
func (l *Log) CheckBreaches(now time.Time) []Alert {
	sort.SliceStable(l.Readings, func(i, j int) bool {
		return l.Readings[i].Timestamp.Before(l.Readings[j].Timestamp)
	})
	var newAlerts []Alert
	i := 0
	for i < len(l.Readings) {
		if InRange(l.Readings[i].TempC) {
			i++
			continue
		}
		start := i
		minT, maxT := l.Readings[i].TempC, l.Readings[i].TempC
		for i < len(l.Readings) && !InRange(l.Readings[i].TempC) {
			if l.Readings[i].TempC < minT {
				minT = l.Readings[i].TempC
			}
			if l.Readings[i].TempC > maxT {
				maxT = l.Readings[i].TempC
			}
			i++
		}
		run := l.Readings[start:i]
		breachStart := run[0].Timestamp
		breachEnd := run[len(run)-1].Timestamp
		if breachEnd.Sub(breachStart) >= AlertWindow {
			seg := run[0].Segment
			if !l.alertExists(seg, breachStart) {
				alert := Alert{
					ID:              fmt.Sprintf("alert-%s-%d", l.BookingID, breachStart.UnixNano()),
					BookingID:       l.BookingID,
					Segment:         seg,
					TriggeredAt:     now,
					BreachStart:     breachStart,
					BreachEnd:       breachEnd,
					MinTemp:         minT,
					MaxTemp:         maxT,
					NotifiedParties: []string{"carrier", "shipper", "temp_service"},
				}
				l.Alerts = append(l.Alerts, alert)
				newAlerts = append(newAlerts, alert)
			}
		}
	}
	return newAlerts
}

func (l *Log) alertExists(seg Segment, start time.Time) bool {
	for _, a := range l.Alerts {
		if a.Segment == seg && a.BreachStart.Equal(start) {
			return true
		}
	}
	return false
}

// FillMissingWithAverage reconstructs readings lost during a recorder outage by
// inserting readings at the standard interval using the average of the
// adjacent recorded values. It is idempotent.
func (l *Log) FillMissingWithAverage() int {
	sort.SliceStable(l.Readings, func(i, j int) bool {
		return l.Readings[i].Timestamp.Before(l.Readings[j].Timestamp)
	})
	if len(l.Readings) < 2 {
		return 0
	}
	filled := make([]Reading, 0, len(l.Readings))
	filled = append(filled, l.Readings[0])
	count := 0
	for i := 1; i < len(l.Readings); i++ {
		prev := l.Readings[i-1]
		curr := l.Readings[i]
		if curr.Timestamp.Sub(prev.Timestamp) > RecordInterval {
			avg := (prev.TempC + curr.TempC) / 2
			t := prev.Timestamp.Add(RecordInterval)
			for t.Before(curr.Timestamp) {
				filled = append(filled, Reading{
					ID:        fmt.Sprintf("%s-filled-%d", l.BookingID, t.UnixNano()),
					BookingID: l.BookingID,
					Segment:   curr.Segment,
					Timestamp: t,
					TempC:     avg,
					Source:    SourceBackup,
					Filled:    true,
				})
				count++
				t = t.Add(RecordInterval)
			}
		}
		filled = append(filled, curr)
	}
	l.Readings = filled
	return count
}

// IsExpired reports whether a temperature log should be purged, given the
// sign-off time and the retention rule (90 days after receipt).
func IsExpired(signedAt, now time.Time) bool {
	return now.Sub(signedAt) > time.Duration(RetentionDays)*24*time.Hour
}
