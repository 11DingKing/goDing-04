package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"arcticfreight/internal/app"
	"arcticfreight/internal/httpapi"
	"arcticfreight/internal/store"
)

func setupHandler(t *testing.T) (http.Handler, *app.Service) {
	t.Helper()
	svc := app.New(store.New(""))
	return httpapi.NewHandler(svc).Routes(), svc
}

func doRequest(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode response failed: %v, body=%s", err, rr.Body.String())
	}
	return m
}

func TestHTTPCreateVoyage(t *testing.T) {
	h, _ := setupHandler(t)
	sailing := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	eta := time.Now().Add(50 * 24 * time.Hour).UTC().Format(time.RFC3339)
	rr := doRequest(t, h, "POST", "/api/voyages", map[string]any{
		"vessel_name": "MV HTTP", "carrier_id": "haijie", "carrier_name": "Haijie Shipping",
		"origin_port": "Ningbo-Zhoushan", "destination_port": "Hamburg",
		"sailing_time": sailing, "eta": eta, "reefer_slots": 5,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decode(t, rr)
	if resp["id"] == nil {
		t.Fatal("expected voyage id")
	}
}

func TestHTTPLockAndConfirmBooking(t *testing.T) {
	h, svc := setupHandler(t)
	vid := createVoyageViaService(t, svc)

	rr := doRequest(t, h, "POST", "/api/bookings", map[string]any{
		"request_id": "http-1", "voyage_id": vid, "shipper_id": "s1", "shipper_name": "Shipper",
		"cargo_type": "power_battery", "container_count": 1,
		"payment_time": time.Now().UTC().Format(time.RFC3339), "deposit_cents": 100000,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	b := decode(t, rr)
	bookingID := b["id"].(string)

	rr2 := doRequest(t, h, "POST", "/api/bookings/"+bookingID+"/confirm", map[string]any{
		"power_circuit_id": "circuit-HTTP-1",
	})
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr2.Code, rr2.Body.String())
	}
	b2 := decode(t, rr2)
	if b2["status"] != "confirmed" {
		t.Fatalf("expected confirmed, got %v", b2["status"])
	}
	if b2["power_circuit_id"] != "circuit-HTTP-1" {
		t.Fatalf("expected power circuit id, got %v", b2["power_circuit_id"])
	}
}

func TestHTTPRecordTemperature(t *testing.T) {
	h, svc := setupHandler(t)
	vid := createVoyageViaService(t, svc)
	b, _ := svc.LockBooking(app.LockBookingRequest{
		RequestID: "temp-1", VoyageID: vid, ShipperID: "s1", ShipperName: "Shipper",
		CargoType: "power_battery", ContainerCount: 1, PaymentTime: time.Now(), DepositCents: 100000,
	})
	svc.ConfirmBooking(b.ID, "circuit-T1")

	rr := doRequest(t, h, "POST", "/api/bookings/"+b.ID+"/temperature", map[string]any{
		"segment": "transit", "timestamp": time.Now().UTC().Format(time.RFC3339), "temp_c": 15.0,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decode(t, rr)
	if resp["reading"] == nil {
		t.Fatal("expected reading in response")
	}
}

func TestHTTPEndToEndFlow(t *testing.T) {
	h, svc := setupHandler(t)
	_ = svc
	sailing := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	eta := time.Now().Add(50 * 24 * time.Hour).UTC().Format(time.RFC3339)

	// 1. create voyage
	rr := doRequest(t, h, "POST", "/api/voyages", map[string]any{
		"vessel_name": "MV E2E", "carrier_id": "haijie", "carrier_name": "Haijie Shipping",
		"origin_port": "Ningbo-Zhoushan", "destination_port": "Hamburg",
		"sailing_time": sailing, "eta": eta, "reefer_slots": 3,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create voyage: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	vid := decode(t, rr)["id"].(string)

	// 2. lock booking
	rr = doRequest(t, h, "POST", "/api/bookings", map[string]any{
		"request_id": "e2e-1", "voyage_id": vid, "shipper_id": "s1", "shipper_name": "Yiwu Storage Co",
		"cargo_type": "energy_storage_cabinet", "container_count": 1,
		"payment_time": time.Now().UTC().Format(time.RFC3339), "deposit_cents": 120000,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("lock: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	bid := decode(t, rr)["id"].(string)

	// 3. confirm + allocate reefer power
	rr = doRequest(t, h, "POST", "/api/bookings/"+bid+"/confirm", map[string]any{"power_circuit_id": "circuit-E2E"})
	if rr.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// 4. load
	rr = doRequest(t, h, "POST", "/api/bookings/"+bid+"/load", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("load: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// 5. transit
	rr = doRequest(t, h, "POST", "/api/bookings/"+bid+"/transit", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("transit: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// 6. record in-range temperatures
	base := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		rr = doRequest(t, h, "POST", "/api/bookings/"+bid+"/temperature", map[string]any{
			"segment": "transit", "timestamp": base.Add(time.Duration(i) * 10 * time.Minute).Format(time.RFC3339), "temp_c": 18.0,
		})
		if rr.Code != http.StatusCreated {
			t.Fatalf("record temp %d: expected 201, got %d: %s", i, rr.Code, rr.Body.String())
		}
	}
	// 7. discharge
	rr = doRequest(t, h, "POST", "/api/bookings/"+bid+"/discharge", map[string]any{
		"timestamp": base.Add(30 * time.Minute).Format(time.RFC3339),
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("discharge: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// 8. complete handover within 2h
	rr = doRequest(t, h, "POST", "/api/handovers/"+bid+"/complete", map[string]any{
		"timestamp": base.Add(60 * time.Minute).Format(time.RFC3339),
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("handover: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// 9. sign receipt — no damage expected
	rr = doRequest(t, h, "POST", "/api/bookings/"+bid+"/sign", map[string]any{
		"timestamp": base.Add(90 * time.Minute).Format(time.RFC3339),
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("sign: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decode(t, rr)
	if resp["has_damage"] != false {
		t.Fatalf("expected no damage, got %v", resp["has_damage"])
	}
}

func createVoyageViaService(t *testing.T, svc *app.Service) string {
	t.Helper()
	v, err := svc.CreateVoyage(app.CreateVoyageRequest{
		VesselName: "MV Test", CarrierID: "haijie", CarrierName: "Haijie Shipping",
		OriginPort: "Ningbo-Zhoushan", DestinationPort: "Hamburg",
		SailingTime: time.Now().Add(30 * 24 * time.Hour),
		ETA:         time.Now().Add(50 * 24 * time.Hour), ReeferSlots: 5,
	})
	if err != nil {
		t.Fatalf("create voyage: %v", err)
	}
	return v.ID
}

// TestHTTPLateCancellationFreesCapacity locks the whole voyage, cancels inside
// the 72 h window over HTTP and expects the capacity to be sellable again.
func TestHTTPLateCancellationFreesCapacity(t *testing.T) {
	h, _ := setupHandler(t)
	sailing := time.Now().Add(48 * time.Hour).UTC()

	rr := doRequest(t, h, "POST", "/api/voyages", map[string]any{
		"vessel_name": "MV Cancel HTTP", "carrier_id": "haijie", "carrier_name": "Haijie Shipping",
		"origin_port": "Ningbo-Zhoushan", "destination_port": "Hamburg",
		"sailing_time": sailing.Format(time.RFC3339),
		"eta":          sailing.Add(19 * 24 * time.Hour).Format(time.RFC3339),
		"reefer_slots": 2,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create voyage: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	vid := decode(t, rr)["id"].(string)

	rr = doRequest(t, h, "POST", "/api/bookings", map[string]any{
		"request_id": "http-cancel-1", "voyage_id": vid, "shipper_id": "s1", "shipper_name": "Ningbo Battery Co",
		"cargo_type": "power_battery", "container_count": 2,
		"payment_time": time.Now().UTC().Format(time.RFC3339), "deposit_cents": 100000,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("lock: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	bid := decode(t, rr)["id"].(string)

	rr = doRequest(t, h, "POST", "/api/bookings/"+bid+"/cancel", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("cancel: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	cancelled := decode(t, rr)
	if cancelled["deposit_forfeited"] != true {
		t.Fatalf("expected the deposit forfeited 48 h before sailing, got %v", cancelled["deposit_forfeited"])
	}

	rr = doRequest(t, h, "GET", "/api/voyages/"+vid, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get voyage: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	v := decode(t, rr)
	if v["booked_reefer_slots"] != float64(0) {
		t.Fatalf("expected 0 booked reefer slots after cancellation, got %v", v["booked_reefer_slots"])
	}

	rr = doRequest(t, h, "POST", "/api/bookings", map[string]any{
		"request_id": "http-cancel-2", "voyage_id": vid, "shipper_id": "s2", "shipper_name": "Waiting Shipper",
		"cargo_type": "energy_storage_cabinet", "container_count": 2,
		"payment_time": time.Now().UTC().Format(time.RFC3339), "deposit_cents": 100000,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("relock freed capacity: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
}
