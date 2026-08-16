package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"arcticfreight/internal/domain/booking"
	"arcticfreight/internal/domain/handover"
	"arcticfreight/internal/domain/temperature"
	"arcticfreight/internal/domain/voyage"
)

// Store is the persistence layer. It keeps all aggregates in memory and
// optionally mirrors state to a JSON file. The embedded RWMutex serialises
// compound operations performed by the application service.
type Store struct {
	mu        sync.RWMutex
	voyages   map[string]*voyage.Voyage
	bookings  map[string]*booking.Booking
	tempLogs  map[string]*temperature.Log
	handovers map[string]*handover.Handover
	dataPath  string
}

// New creates a store. If dataPath is non-empty the store loads existing state
// and persists mutations to that file.
func New(dataPath string) *Store {
	s := &Store{
		voyages:   make(map[string]*voyage.Voyage),
		bookings:  make(map[string]*booking.Booking),
		tempLogs:  make(map[string]*temperature.Log),
		handovers: make(map[string]*handover.Handover),
		dataPath:  dataPath,
	}
	if dataPath != "" {
		s.load()
	}
	return s
}

// Lock, Unlock, RLock and RUnlock expose the mutex so the application service
// can perform atomic compound operations.
func (s *Store) Lock()    { s.mu.Lock() }
func (s *Store) Unlock()  { s.mu.Unlock() }
func (s *Store) RLock()   { s.mu.RLock() }
func (s *Store) RUnlock() { s.mu.RUnlock() }

// --- Voyages ---

func (s *Store) GetVoyage(id string) (*voyage.Voyage, bool) {
	v, ok := s.voyages[id]
	return v, ok
}

func (s *Store) SaveVoyage(v *voyage.Voyage) {
	s.voyages[v.ID] = v
}

func (s *Store) ListVoyages() []*voyage.Voyage {
	out := make([]*voyage.Voyage, 0, len(s.voyages))
	for _, v := range s.voyages {
		out = append(out, v)
	}
	return out
}

// --- Bookings ---

func (s *Store) GetBooking(id string) (*booking.Booking, bool) {
	b, ok := s.bookings[id]
	return b, ok
}

func (s *Store) SaveBooking(b *booking.Booking) {
	s.bookings[b.ID] = b
}

func (s *Store) ListBookings() []*booking.Booking {
	out := make([]*booking.Booking, 0, len(s.bookings))
	for _, b := range s.bookings {
		out = append(out, b)
	}
	return out
}

// --- Temperature logs ---

func (s *Store) GetTempLog(bookingID string) (*temperature.Log, bool) {
	l, ok := s.tempLogs[bookingID]
	return l, ok
}

func (s *Store) GetOrCreateTempLog(bookingID string) *temperature.Log {
	if l, ok := s.tempLogs[bookingID]; ok {
		return l
	}
	l := temperature.NewLog(bookingID)
	s.tempLogs[bookingID] = l
	return l
}

func (s *Store) ListTempLogs() []*temperature.Log {
	out := make([]*temperature.Log, 0, len(s.tempLogs))
	for _, l := range s.tempLogs {
		out = append(out, l)
	}
	return out
}

// --- Handovers ---

func (s *Store) GetHandover(bookingID string) (*handover.Handover, bool) {
	h, ok := s.handovers[bookingID]
	return h, ok
}

func (s *Store) SaveHandover(h *handover.Handover) {
	s.handovers[h.BookingID] = h
}

func (s *Store) ListHandovers() []*handover.Handover {
	out := make([]*handover.Handover, 0, len(s.handovers))
	for _, h := range s.handovers {
		out = append(out, h)
	}
	return out
}

// --- Persistence ---

type snapshot struct {
	Voyages   map[string]*voyage.Voyage     `json:"voyages"`
	Bookings  map[string]*booking.Booking   `json:"bookings"`
	TempLogs  map[string]*temperature.Log   `json:"temp_logs"`
	Handovers map[string]*handover.Handover `json:"handovers"`
}

// Save serialises the entire state to the data file. Must be called under the
// write lock.
func (s *Store) Save() {
	if s.dataPath == "" {
		return
	}
	snap := snapshot{
		Voyages:   s.voyages,
		Bookings:  s.bookings,
		TempLogs:  s.tempLogs,
		Handovers: s.handovers,
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	if dir := filepath.Dir(s.dataPath); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	_ = os.WriteFile(s.dataPath, data, 0o644)
}

func (s *Store) load() {
	data, err := os.ReadFile(s.dataPath)
	if err != nil {
		return
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return
	}
	if snap.Voyages != nil {
		s.voyages = snap.Voyages
	}
	if snap.Bookings != nil {
		s.bookings = snap.Bookings
	}
	if snap.TempLogs != nil {
		s.tempLogs = snap.TempLogs
	}
	if snap.Handovers != nil {
		s.handovers = snap.Handovers
	}
}
