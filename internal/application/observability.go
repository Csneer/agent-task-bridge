package application

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Csneer/agent-task-bridge/internal/domain"
)

// Readiness probes are excluded from HTTP observations and counters.
// HTTPObservation contains only bounded labels, never request or response data.
// Method is a known RPC method or "unknown" (also used for non-RPC requests).
// ErrorKind is empty on success; Status is the HTTP response status, so a
// JSON-RPC failure can have Status 200 and a nonempty ErrorKind.
// Duration measures handling time, excluding the observer itself.
type HTTPObservation struct {
	Method    string
	Status    int
	ErrorKind string
	Duration  time.Duration
}

// MaintenanceObservation describes one complete Maintain call, including the
// startup call. ErrorKind is empty on success and never contains error text.
type MaintenanceObservation struct {
	ErrorKind string
	Duration  time.Duration
}

func isolateObserver(observe func()) {
	defer func() { _ = recover() }()
	observe()
}

func observedMethod(method string) string {
	if writeMethods[method] {
		return method
	}
	switch method {
	case "bridge.capabilities", "task.get", "record.get", "task.discover", "snapshot.open", "snapshot.page", "events.open", "events.read", "receipt.get":
		return method
	default:
		return "unknown"
	}
}

func observationErrorKind(kind string) string {
	if _, ok := domain.Codes[kind]; ok {
		return kind
	}
	if kind == "TRANSPORT_REJECTED" {
		return kind
	}
	return "INTERNAL_ERROR"
}

type observedResponse struct {
	http.ResponseWriter
	observation HTTPObservation
	wroteHeader bool
}

func (w *observedResponse) WriteHeader(status int) {
	if !w.wroteHeader {
		w.observation.Status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *observedResponse) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func observeError(w http.ResponseWriter, kind string) {
	if observed, ok := w.(*observedResponse); ok {
		observed.observation.ErrorKind = observationErrorKind(kind)
	}
}

// Stats is a process-local cumulative snapshot. Fields are individually atomic;
// concurrent activity may advance between field reads. Readiness is excluded.
type Stats struct {
	TotalRequests       uint64
	Errors              uint64
	MaintenanceRuns     uint64
	MaintenanceFailures uint64
}

type serviceCounters struct {
	requests            atomic.Uint64
	errors              atomic.Uint64
	maintenance         atomic.Uint64
	maintenanceFailures atomic.Uint64
}

func (s *Service) Stats() Stats {
	return Stats{TotalRequests: s.counters.requests.Load(), Errors: s.counters.errors.Load(), MaintenanceRuns: s.counters.maintenance.Load(), MaintenanceFailures: s.counters.maintenanceFailures.Load()}
}
