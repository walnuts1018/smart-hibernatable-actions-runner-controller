package utils

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

const (
	odataIDKey = "@odata.id"
	nameKey    = "Name"
)

// FakeRedfishServer emulates a physical machine's BMC Redfish interface for E2E and unit testing.
type FakeRedfishServer struct {
	mu            sync.RWMutex
	Server        *httptest.Server
	PowerState    string // "On", "Off", "PoweringOn", "PoweringOff"
	ResetCount    int
	LastResetType string
	FailNextReset bool
	Delay         time.Duration
}

// NewFakeRedfishServer creates and starts a new FakeRedfishServer.
func NewFakeRedfishServer(initialPowerState string) *FakeRedfishServer {
	if initialPowerState == "" {
		initialPowerState = "Off"
	}
	f := &FakeRedfishServer{
		PowerState: initialPowerState,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/redfish/v1/", f.handleServiceRoot)
	mux.HandleFunc("/redfish/v1/Systems", f.handleSystems)
	mux.HandleFunc("/redfish/v1/Systems/1", f.handleSystem)
	mux.HandleFunc("/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", f.handleReset)

	f.Server = httptest.NewServer(mux)
	return f
}

// Close shuts down the test server.
func (f *FakeRedfishServer) Close() {
	if f.Server != nil {
		f.Server.Close()
	}
}

// URL returns the base URL of the fake BMC.
func (f *FakeRedfishServer) URL() string {
	if f.Server != nil {
		return f.Server.URL
	}
	return ""
}

func (f *FakeRedfishServer) handleServiceRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/redfish/v1" && r.URL.Path != "/redfish/v1/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		odataIDKey: "/redfish/v1",
		"Id":       "RootService",
		nameKey:    "Root Service",
		"Systems": map[string]string{
			odataIDKey: "/redfish/v1/Systems",
		},
	})
}

func (f *FakeRedfishServer) handleSystems(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		odataIDKey:            "/redfish/v1/Systems",
		nameKey:               "Computer Systems Collection",
		"Members@odata.count": 1,
		"Members": []map[string]string{
			{odataIDKey: "/redfish/v1/Systems/1"},
		},
	})
}

func (f *FakeRedfishServer) handleSystem(w http.ResponseWriter, _ *http.Request) {
	f.mu.RLock()
	state := f.PowerState
	f.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		odataIDKey:   "/redfish/v1/Systems/1",
		"Id":         "1",
		nameKey:      "System-1",
		"PowerState": state,
		"Actions": map[string]any{
			"#ComputerSystem.Reset": map[string]any{
				"target": "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
				"ResetType@Redfish.AllowableValues": []string{
					"On",
					"GracefulShutdown",
					"ForceOff",
					"PushPowerButton",
				},
			},
		},
	})
}

type resetPayload struct {
	ResetType string `json:"ResetType"`
}

func (f *FakeRedfishServer) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var p resetPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.FailNextReset {
		f.FailNextReset = false
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	f.ResetCount++
	f.LastResetType = p.ResetType

	switch p.ResetType {
	case "On":
		f.PowerState = "On"
	case "GracefulShutdown", "ForceOff", "PushPowerButton":
		f.PowerState = "Off"
	default:
		http.Error(w, "Invalid ResetType", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
