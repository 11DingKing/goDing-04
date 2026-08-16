# ArcticFreight — 中欧北极快航运力与温敏货物协同保障后端

ArcticFreight is a self-contained Go backend that coordinates reefer slot
booking, carrier confirmation, port handover and temperature monitoring for the
China-Europe Arctic fast-route peak-season service.

## Participants modelled

- **承运方** Haijie Shipping (海杰航运)
- **货主** power-battery and energy-storage-cabinet shippers in Ningbo / Yiwu
- **港口节点** Ningbo-Zhoushan (origin) and Hamburg (destination)
- **温控服务商** third-party temperature service

## Workflows

1. **货主在线锁舱** — shipper locks reefer capacity (payment-time ordered, no overbooking).
2. **承运方确认舱位并分配冷藏电源** — carrier confirms and assigns a power circuit.
3. **港口装卸与转运衔接** — load → transit → discharge → temperature handover → sign.
4. **温控服务商分段上报温度** — per-segment readings every 10 minutes.

## Business rules

- Storage cabinets and power batteries must stay within **0–25 °C**; a breach
  lasting **≥ 15 minutes** alerts all three parties.
- Cancellation must be requested **≥ 72 h** before sailing; later cancellations
  forfeit the deposit. Cancelling always returns the reefer slots to the
  voyage's pool so the capacity can be resold, whatever happens to the deposit.
- Hamburg temperature handover must complete within **2 h** of discharge;
  overdue handovers escalate to manual intervention.
- Temperature records are kept every **10 minutes** and retained for **90 days**
  after sign-off.
- Cargo-damage liability is determined per segment from the temperature record.
- Concurrent lock requests are serialised by **payment time**; overbooking is
  forbidden.
- On recorder power loss the system switches to the **backup recorder** and
  reconstructs missing readings using the average of adjacent records.

## Quick start

```bash
go run ./cmd/server                 # listens on :51108
```

Configuration is via environment variables:

| Variable | Default | Description |
|---|---|---|
| `ARCTICFREIGHT_PORT` | `51108` | HTTP listen port |
| `ARCTICFREIGHT_DATA_PATH` | `/tmp/arcticfreight-state.json` | JSON state file (empty = in-memory) |
| `ARCTICFREIGHT_CHECK_INTERVAL` | `1m` | Background check interval |

## Main HTTP API

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/voyages` | Create a voyage |
| `GET` | `/api/voyages` | List voyages |
| `GET` | `/api/voyages/{id}` | Get a voyage |
| `POST` | `/api/bookings` | Lock a single booking |
| `POST` | `/api/bookings/batch` | Lock multiple bookings (payment-time ordered) |
| `GET` | `/api/bookings/{id}` | Get a booking |
| `POST` | `/api/bookings/{id}/confirm` | Confirm + allocate reefer power |
| `POST` | `/api/bookings/{id}/cancel` | Cancel (72 h rule) |
| `POST` | `/api/bookings/{id}/load` | Load cargo |
| `POST` | `/api/bookings/{id}/transit` | Start transit |
| `POST` | `/api/bookings/{id}/discharge` | Discharge at destination |
| `POST` | `/api/bookings/{id}/sign` | Sign receipt + damage report |
| `POST` | `/api/bookings/{id}/temperature` | Record a temperature reading |
| `GET` | `/api/bookings/{id}/temperature` | Get temperature log |
| `POST` | `/api/bookings/{id}/recorder/disconnect` | Switch to backup recorder |
| `POST` | `/api/bookings/{id}/temperature/recover` | Reconstruct missing readings |
| `POST` | `/api/handovers/{bookingId}/complete` | Complete temperature handover |

Example:

```bash
curl -s -X POST localhost:51108/api/voyages \
  -H 'Content-Type: application/json' \
  -d '{"vessel_name":"MV Arctic Star","carrier_id":"haijie","carrier_name":"Haijie Shipping","origin_port":"Ningbo-Zhoushan","destination_port":"Hamburg","sailing_time":"2026-12-01T00:00:00Z","eta":"2026-12-20T00:00:00Z","reefer_slots":10}'
```

## Testing

```bash
go test -timeout=120s -count=1 ./...
```

## Docker

```bash
docker build -t arcticfreight .
docker run --rm -p 51108:51108 arcticfreight
```

Multi-arch build (amd64 + arm64):

```bash
docker buildx build --platform linux/amd64,linux/arm64 -t arcticfreight .
```
