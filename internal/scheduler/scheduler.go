package scheduler

import (
	"time"

	"arcticfreight/internal/app"
)

// Scheduler runs background tasks: periodic temperature-breach detection and
// handover-timeout escalation to manual intervention.
type Scheduler struct {
	service  *app.Service
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
}

// New creates a scheduler that ticks every interval.
func New(svc *app.Service, interval time.Duration) *Scheduler {
	return &Scheduler{
		service:  svc,
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start launches the background loop.
func (sc *Scheduler) Start() {
	go sc.run()
}

func (sc *Scheduler) run() {
	defer close(sc.done)
	ticker := time.NewTicker(sc.interval)
	defer ticker.Stop()
	for {
		select {
		case <-sc.stop:
			return
		case <-ticker.C:
			now := time.Now().UTC()
			sc.service.CheckTemperatureBreaches(now)
			sc.service.CheckHandoverTimeouts(now)
		}
	}
}

// Stop signals the loop to exit and waits for it.
func (sc *Scheduler) Stop() {
	select {
	case <-sc.stop:
		// already stopped
	default:
		close(sc.stop)
	}
	<-sc.done
}
