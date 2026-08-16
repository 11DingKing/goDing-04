package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"arcticfreight/internal/app"
	"arcticfreight/internal/domain/booking"
	"arcticfreight/internal/domain/temperature"
)

// Handler exposes the application service over JSON HTTP.
type Handler struct {
	svc *app.Service
}

func NewHandler(svc *app.Service) *Handler {
	return &Handler{svc: svc}
}

// Routes builds the mux with all endpoints.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.health)

	mux.HandleFunc("POST /api/voyages", h.createVoyage)
	mux.HandleFunc("GET /api/voyages", h.listVoyages)
	mux.HandleFunc("GET /api/voyages/{id}", h.getVoyage)

	mux.HandleFunc("POST /api/bookings", h.lockBooking)
	mux.HandleFunc("POST /api/bookings/batch", h.lockBookingsBatch)
	mux.HandleFunc("GET /api/bookings/{id}", h.getBooking)
	mux.HandleFunc("POST /api/bookings/{id}/confirm", h.confirmBooking)
	mux.HandleFunc("POST /api/bookings/{id}/cancel", h.cancelBooking)
	mux.HandleFunc("POST /api/bookings/{id}/load", h.loadCargo)
	mux.HandleFunc("POST /api/bookings/{id}/transit", h.startTransit)
	mux.HandleFunc("POST /api/bookings/{id}/discharge", h.dischargeCargo)
	mux.HandleFunc("POST /api/bookings/{id}/sign", h.signReceipt)

	mux.HandleFunc("POST /api/bookings/{id}/temperature", h.recordTemperature)
	mux.HandleFunc("GET /api/bookings/{id}/temperature", h.getTemperature)
	mux.HandleFunc("POST /api/bookings/{id}/recorder/disconnect", h.reportDisconnect)
	mux.HandleFunc("POST /api/bookings/{id}/temperature/recover", h.recoverTemperature)

	mux.HandleFunc("POST /api/handovers/{bookingId}/complete", h.completeHandover)
	return mux
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("time is required")
	}
	return time.Parse(time.RFC3339, s)
}

// optionalNow parses a timestamp from the request body, falling back to now.
func optionalNow(body []byte) (time.Time, error) {
	var req struct {
		Timestamp string `json:"timestamp"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return time.Time{}, err
		}
	}
	if req.Timestamp == "" {
		return time.Now().UTC(), nil
	}
	return parseTime(req.Timestamp)
}

func readBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 1024)
	for {
		n, err := r.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return buf
}

// --- handlers ---

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type createVoyageRequest struct {
	VesselName      string `json:"vessel_name"`
	CarrierID       string `json:"carrier_id"`
	CarrierName     string `json:"carrier_name"`
	OriginPort      string `json:"origin_port"`
	DestinationPort string `json:"destination_port"`
	SailingTime     string `json:"sailing_time"`
	ETA             string `json:"eta"`
	ReeferSlots     int    `json:"reefer_slots"`
}

func (h *Handler) createVoyage(w http.ResponseWriter, r *http.Request) {
	var req createVoyageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sailing, err := parseTime(req.SailingTime)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid sailing_time")
		return
	}
	eta, err := parseTime(req.ETA)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid eta")
		return
	}
	v, err := h.svc.CreateVoyage(app.CreateVoyageRequest{
		VesselName:      req.VesselName,
		CarrierID:       req.CarrierID,
		CarrierName:     req.CarrierName,
		OriginPort:      req.OriginPort,
		DestinationPort: req.DestinationPort,
		SailingTime:     sailing,
		ETA:             eta,
		ReeferSlots:     req.ReeferSlots,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) listVoyages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.svc.ListVoyages())
}

func (h *Handler) getVoyage(w http.ResponseWriter, r *http.Request) {
	v, err := h.svc.GetVoyage(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type lockBookingRequest struct {
	RequestID      string `json:"request_id"`
	VoyageID       string `json:"voyage_id"`
	ShipperID      string `json:"shipper_id"`
	ShipperName    string `json:"shipper_name"`
	CargoType      string `json:"cargo_type"`
	ContainerCount int    `json:"container_count"`
	PaymentTime    string `json:"payment_time"`
	DepositCents   int64  `json:"deposit_cents"`
}

func (h *Handler) lockBooking(w http.ResponseWriter, r *http.Request) {
	var req lockBookingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	pt, err := parseTime(req.PaymentTime)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid payment_time")
		return
	}
	b, err := h.svc.LockBooking(app.LockBookingRequest{
		RequestID:      req.RequestID,
		VoyageID:       req.VoyageID,
		ShipperID:      req.ShipperID,
		ShipperName:    req.ShipperName,
		CargoType:      booking.CargoType(req.CargoType),
		ContainerCount: req.ContainerCount,
		PaymentTime:    pt,
		DepositCents:   req.DepositCents,
	})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (h *Handler) lockBookingsBatch(w http.ResponseWriter, r *http.Request) {
	var reqs []lockBookingRequest
	if err := json.NewDecoder(r.Body).Decode(&reqs); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	appReqs := make([]app.LockBookingRequest, len(reqs))
	for i, req := range reqs {
		pt, err := parseTime(req.PaymentTime)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid payment_time at index %d", i))
			return
		}
		appReqs[i] = app.LockBookingRequest{
			RequestID:      req.RequestID,
			VoyageID:       req.VoyageID,
			ShipperID:      req.ShipperID,
			ShipperName:    req.ShipperName,
			CargoType:      booking.CargoType(req.CargoType),
			ContainerCount: req.ContainerCount,
			PaymentTime:    pt,
			DepositCents:   req.DepositCents,
		}
	}
	results, err := h.svc.LockBookings(appReqs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := make([]map[string]any, len(results))
	for i, res := range results {
		if res.Err != nil {
			resp[i] = map[string]any{"error": res.Err.Error()}
		} else {
			resp[i] = map[string]any{"booking": res.Booking}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getBooking(w http.ResponseWriter, r *http.Request) {
	b, err := h.svc.GetBooking(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (h *Handler) confirmBooking(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PowerCircuitID string `json:"power_circuit_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	b, err := h.svc.ConfirmBooking(r.PathValue("id"), req.PowerCircuitID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (h *Handler) cancelBooking(w http.ResponseWriter, r *http.Request) {
	now, err := optionalNow(readBody(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid timestamp")
		return
	}
	b, err := h.svc.CancelBooking(r.PathValue("id"), now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (h *Handler) loadCargo(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.LoadCargo(r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	b, _ := h.svc.GetBooking(r.PathValue("id"))
	writeJSON(w, http.StatusOK, b)
}

func (h *Handler) startTransit(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.StartTransit(r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	b, _ := h.svc.GetBooking(r.PathValue("id"))
	writeJSON(w, http.StatusOK, b)
}

func (h *Handler) dischargeCargo(w http.ResponseWriter, r *http.Request) {
	now, err := optionalNow(readBody(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid timestamp")
		return
	}
	if err := h.svc.DischargeCargo(r.PathValue("id"), now); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	b, _ := h.svc.GetBooking(r.PathValue("id"))
	writeJSON(w, http.StatusOK, b)
}

func (h *Handler) signReceipt(w http.ResponseWriter, r *http.Request) {
	now, err := optionalNow(readBody(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid timestamp")
		return
	}
	report, err := h.svc.SignReceipt(r.PathValue("id"), now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *Handler) recordTemperature(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Segment string  `json:"segment"`
		Time    string  `json:"timestamp"`
		TempC   float64 `json:"temp_c"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ts, err := parseTime(req.Time)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid timestamp")
		return
	}
	reading, alerts, err := h.svc.RecordTemperature(r.PathValue("id"), temperature.Segment(req.Segment), ts, req.TempC)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"reading": reading,
		"alerts":  alerts,
	})
}

func (h *Handler) getTemperature(w http.ResponseWriter, r *http.Request) {
	log, err := h.svc.GetTemperatureLog(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, log)
}

func (h *Handler) reportDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.ReportRecorderDisconnect(r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "switched_to_backup"})
}

func (h *Handler) recoverTemperature(w http.ResponseWriter, r *http.Request) {
	filled, err := h.svc.RecoverTemperatureData(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"filled_readings": filled})
}

func (h *Handler) completeHandover(w http.ResponseWriter, r *http.Request) {
	now, err := optionalNow(readBody(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid timestamp")
		return
	}
	ho, err := h.svc.CompleteHandover(r.PathValue("bookingId"), now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ho)
}
