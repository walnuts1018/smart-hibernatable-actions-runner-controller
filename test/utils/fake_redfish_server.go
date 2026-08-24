package utils

import (
	"encoding/json"
	"encoding/pem"
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
	mu                  sync.RWMutex
	Server              *httptest.Server
	PowerState          string // "On", "Off", "PoweringOn", "PoweringOff"
	ResetCount          int
	LastResetType       string
	FailNextReset       bool
	FailWithUnsupported bool
	Delay               time.Duration
	AllowableResetTypes []string
	SystemsCount        int
}

// NewFakeRedfishServer creates and starts a new FakeRedfishServer with TLS.
func NewFakeRedfishServer(initialPowerState string) *FakeRedfishServer {
	if initialPowerState == "" {
		initialPowerState = "Off"
	}
	f := &FakeRedfishServer{
		PowerState: initialPowerState,
		AllowableResetTypes: []string{
			"On",
			"ForceOn",
			"GracefulShutdown",
			"ForceOff",
			"PushPowerButton",
		},
		SystemsCount: 1,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/redfish/v1/", f.handleServiceRoot)
	mux.HandleFunc("/redfish/v1/Systems", f.handleSystems)
	mux.HandleFunc("/redfish/v1/Systems/1", f.handleSystem)
	mux.HandleFunc("/redfish/v1/Systems/2", f.handleSystem2)
	mux.HandleFunc("/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", f.handleReset)
	mux.HandleFunc("/redfish/v1/Systems/2/Actions/ComputerSystem.Reset", f.handleReset)

	f.Server = httptest.NewTLSServer(mux)
	return f
}

// Close shuts down the test server.
func (f *FakeRedfishServer) Close() {
	if f.Server != nil {
		f.Server.Close()
	}
}

// URL returns the HTTPS base URL of the fake BMC.
func (f *FakeRedfishServer) URL() string {
	if f.Server != nil {
		return f.Server.URL
	}
	return ""
}

// CACertPEM returns the PEM-encoded CA certificate for the test server.
func (f *FakeRedfishServer) CACertPEM() []byte {
	if f.Server != nil && f.Server.Certificate() != nil {
		return pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: f.Server.Certificate().Raw,
		})
	}
	return nil
}

func (f *FakeRedfishServer) handleServiceRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/redfish/v1" && r.URL.Path != "/redfish/v1/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		odataIDKey: "/redfish/v1",
		"Id":       "RootService",
		nameKey:    "Root Service",
		"Systems": map[string]string{
			odataIDKey: "/redfish/v1/Systems",
		},
	})
}

func (f *FakeRedfishServer) handleSystems(w http.ResponseWriter, _ *http.Request) {
	f.mu.RLock()
	count := f.SystemsCount
	f.mu.RUnlock()

	members := []map[string]string{
		{odataIDKey: "/redfish/v1/Systems/1"},
	}
	if count > 1 {
		members = append(members, map[string]string{
			odataIDKey: "/redfish/v1/Systems/2",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		odataIDKey:            "/redfish/v1/Systems",
		nameKey:               "Computer Systems Collection",
		"Members@odata.count": len(members),
		"Members":             members,
	})
}

func (f *FakeRedfishServer) handleSystem(w http.ResponseWriter, _ *http.Request) {
	f.mu.RLock()
	state := f.PowerState
	allowable := f.AllowableResetTypes
	f.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		odataIDKey:   "/redfish/v1/Systems/1",
		"Id":         "1",
		nameKey:      "System-1",
		"PowerState": state,
	}

	if allowable != nil {
		resp["Actions"] = map[string]any{
			"#ComputerSystem.Reset": map[string]any{
				"target":                            "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
				"ResetType@Redfish.AllowableValues": allowable,
			},
		}
	} else {
		resp["Actions"] = map[string]any{
			"#ComputerSystem.Reset": map[string]any{
				"target": "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
			},
		}
	}

	json.NewEncoder(w).Encode(resp)
}

func (f *FakeRedfishServer) handleSystem2(w http.ResponseWriter, _ *http.Request) {
	f.mu.RLock()
	state := f.PowerState
	allowable := f.AllowableResetTypes
	f.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		odataIDKey:   "/redfish/v1/Systems/2",
		"Id":         "2",
		nameKey:      "System-2",
		"PowerState": state,
	}

	if allowable != nil {
		resp["Actions"] = map[string]any{
			"#ComputerSystem.Reset": map[string]any{
				"target":                            "/redfish/v1/Systems/2/Actions/ComputerSystem.Reset",
				"ResetType@Redfish.AllowableValues": allowable,
			},
		}
	} else {
		resp["Actions"] = map[string]any{
			"#ComputerSystem.Reset": map[string]any{
				"target": "/redfish/v1/Systems/2/Actions/ComputerSystem.Reset",
			},
		}
	}

	json.NewEncoder(w).Encode(resp)
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

	if f.FailWithUnsupported {
		f.FailWithUnsupported = false
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "Base.1.0.ActionParameterNotSupported",
				"message": "ResetType value is unsupported",
				"@Message.ExtendedInfo": []map[string]any{
					{
						"MessageId": "Base.1.0.ActionParameterNotSupported",
						"Message":   "ActionParameterNotSupported",
					},
				},
			},
		})
		return
	}

	if f.FailNextReset {
		f.FailNextReset = false
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	f.ResetCount++
	f.LastResetType = p.ResetType

	switch p.ResetType {
	case "On", "ForceOn":
		f.PowerState = "On"
	case "GracefulShutdown", "ForceOff", "PushPowerButton":
		f.PowerState = "Off"
	default:
		http.Error(w, "Invalid ResetType", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

