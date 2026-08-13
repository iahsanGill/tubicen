package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestScenarioChangesMetrics(t *testing.T) {
	t.Parallel()
	service := &demoService{}
	request, err := http.NewRequest(http.MethodPost, "/scenario/degraded", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.SetPathValue("name", "degraded")
	response := newResponseRecorder()
	service.setScenario(response, request)
	if response.status != http.StatusOK {
		t.Fatalf("status = %d", response.status)
	}

	request, err = http.NewRequest(http.MethodGet, "/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	response = newResponseRecorder()
	service.metrics(response, request)
	if !strings.Contains(response.body.String(), `checkout_error_ratio{service="checkout",environment="production"} 0.20`) {
		t.Fatalf("unexpected metrics:\n%s", response.body.String())
	}
}

func TestAlertmanagerWebhookIsStored(t *testing.T) {
	t.Parallel()
	service := &demoService{}
	request, err := http.NewRequest(http.MethodPost, "/alerts", strings.NewReader(`{"status":"firing","alerts":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	response := newResponseRecorder()
	service.receiveAlerts(response, request)
	if response.status != http.StatusNoContent {
		t.Fatalf("status = %d", response.status)
	}

	request, err = http.NewRequest(http.MethodGet, "/notifications", nil)
	if err != nil {
		t.Fatal(err)
	}
	response = newResponseRecorder()
	service.listNotifications(response, request)
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(&response.body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 1 {
		t.Fatalf("notifications = %d, want 1", payload.Count)
	}
}

type responseRecorder struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{header: make(http.Header), status: http.StatusOK}
}

func (recorder *responseRecorder) Header() http.Header    { return recorder.header }
func (recorder *responseRecorder) WriteHeader(status int) { recorder.status = status }
func (recorder *responseRecorder) Write(content []byte) (int, error) {
	return recorder.body.Write(content)
}

var _ io.Writer = (*responseRecorder)(nil)
