package utils

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
)

// FakeScaleSetServer emulates the GitHub Actions scale set backend.
type FakeScaleSetServer struct {
	mu           sync.RWMutex
	Server       *httptest.Server
	ScaleSets    map[int64]map[string]interface{}
	NextID       int64
	AssignedJobs int
	RunningJobs  int
}

// NewFakeScaleSetServer creates and starts a new FakeScaleSetServer.
func NewFakeScaleSetServer() *FakeScaleSetServer {
	f := &FakeScaleSetServer{
		ScaleSets: make(map[int64]map[string]interface{}),
		NextID:    100,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/actions/runner-scale-sets", f.handleScaleSets)
	mux.HandleFunc("/api/v3/actions/runner-scale-sets/", f.handleScaleSetSubresources)
	mux.HandleFunc("/test/demand", f.handleDemand)

	f.Server = httptest.NewServer(mux)
	return f
}

// Close shuts down the fake GitHub server.
func (f *FakeScaleSetServer) Close() {
	if f.Server != nil {
		f.Server.Close()
	}
}

// URL returns the test server URL.
func (f *FakeScaleSetServer) URL() string {
	if f.Server != nil {
		return f.Server.URL
	}
	return ""
}

// SetDemand updates the simulated job demand.
func (f *FakeScaleSetServer) SetDemand(assigned, running int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.AssignedJobs = assigned
	f.RunningJobs = running
}

func (f *FakeScaleSetServer) handleScaleSets(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if r.Method == http.MethodPost {
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)

		id := f.NextID
		f.NextID++

		ss := map[string]interface{}{
			"id":           id,
			"name":         req["name"],
			"runner_group": req["runner_group_name"],
		}
		f.ScaleSets[id] = ss

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(ss)
		return
	}

	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

func (f *FakeScaleSetServer) handleScaleSetSubresources(w http.ResponseWriter, r *http.Request) {
	// e.g. /api/v3/actions/runner-scale-sets/100/generate-jitconfig
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"runner": map[string]interface{}{
			"id":   1,
			"name": "test-runner",
		},
		"encoded_jit_config": "fake-jit-config-base64",
	})
}

func (f *FakeScaleSetServer) handleDemand(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if r.Method == http.MethodPost {
		var body struct {
			Assigned int `json:"assigned"`
			Running  int `json:"running"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			f.AssignedJobs = body.Assigned
			f.RunningJobs = body.Running
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"assigned": f.AssignedJobs,
		"running":  f.RunningJobs,
	})
}
