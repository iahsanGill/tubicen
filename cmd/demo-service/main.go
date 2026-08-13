package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	healthyErrorRatio  = 0.01
	degradedErrorRatio = 0.20
)

type demoService struct {
	degraded     atomic.Bool
	successCount atomic.Uint64
	errorCount   atomic.Uint64

	notificationsMu sync.RWMutex
	notifications   []json.RawMessage
}

func main() {
	address := ":8080"
	if value := strings.TrimSpace(os.Getenv("LISTEN_ADDR")); value != "" {
		address = value
	}

	service := &demoService{}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go service.generateTraffic(ctx)

	server := &http.Server{
		Addr:              address,
		Handler:           service.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("checkout demo service listening on %s", address)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func (service *demoService) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", service.home)
	mux.HandleFunc("GET /healthz", service.health)
	mux.HandleFunc("GET /metrics", service.metrics)
	mux.HandleFunc("GET /state", service.state)
	mux.HandleFunc("POST /scenario/{name}", service.setScenario)
	mux.HandleFunc("POST /alerts", service.receiveAlerts)
	mux.HandleFunc("GET /notifications", service.listNotifications)
	return mux
}

func (service *demoService) generateTraffic(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if service.degraded.Load() {
				service.successCount.Add(80)
				service.errorCount.Add(20)
			} else {
				service.successCount.Add(99)
				service.errorCount.Add(1)
			}
		}
	}
}

func (service *demoService) errorRatio() float64 {
	if service.degraded.Load() {
		return degradedErrorRatio
	}
	return healthyErrorRatio
}

func (service *demoService) scenario() string {
	if service.degraded.Load() {
		return "degraded"
	}
	return "healthy"
}

func (service *demoService) home(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "Tubicen production demo - checkout-api")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "GET  /metrics")
	fmt.Fprintln(w, "GET  /state")
	fmt.Fprintln(w, "POST /scenario/healthy")
	fmt.Fprintln(w, "POST /scenario/degraded")
	fmt.Fprintln(w, "GET  /notifications")
}

func (service *demoService) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}

func (service *demoService) state(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service":     "checkout",
		"environment": "production",
		"scenario":    service.scenario(),
		"error_ratio": service.errorRatio(),
	})
}

func (service *demoService) setScenario(w http.ResponseWriter, request *http.Request) {
	switch request.PathValue("name") {
	case "healthy":
		service.degraded.Store(false)
	case "degraded":
		service.degraded.Store(true)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scenario must be healthy or degraded"})
		return
	}
	service.state(w, request)
}

func (service *demoService) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	ratio := strconv.FormatFloat(service.errorRatio(), 'f', 2, 64)
	fmt.Fprintln(w, "# HELP checkout_error_ratio Current checkout request error ratio.")
	fmt.Fprintln(w, "# TYPE checkout_error_ratio gauge")
	fmt.Fprintf(w, "checkout_error_ratio{service=\"checkout\",environment=\"production\"} %s\n", ratio)
	fmt.Fprintln(w, "# HELP checkout_requests_total Simulated checkout requests processed.")
	fmt.Fprintln(w, "# TYPE checkout_requests_total counter")
	fmt.Fprintf(w, "checkout_requests_total{service=\"checkout\",environment=\"production\",status=\"200\"} %d\n", service.successCount.Load())
	fmt.Fprintf(w, "checkout_requests_total{service=\"checkout\",environment=\"production\",status=\"500\"} %d\n", service.errorCount.Load())
}

func (service *demoService) receiveAlerts(w http.ResponseWriter, request *http.Request) {
	defer request.Body.Close()
	var payload json.RawMessage
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 1<<20))
	if err := decoder.Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Alertmanager payload"})
		return
	}
	service.notificationsMu.Lock()
	service.notifications = append(service.notifications, append(json.RawMessage(nil), payload...))
	count := len(service.notifications)
	service.notificationsMu.Unlock()
	log.Printf("received Alertmanager webhook notification %d", count)
	w.WriteHeader(http.StatusNoContent)
}

func (service *demoService) listNotifications(w http.ResponseWriter, _ *http.Request) {
	service.notificationsMu.RLock()
	items := make([]json.RawMessage, len(service.notifications))
	copy(items, service.notifications)
	service.notificationsMu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"count": len(items), "notifications": items})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
