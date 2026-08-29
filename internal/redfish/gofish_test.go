package redfish

import (
	"context"
	"strings"
	"testing"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/test/utils"
)

func TestNewGofishController_Validation(t *testing.T) {
	fakeBMC := utils.NewFakeRedfishServer("Off")
	defer fakeBMC.Close()

	t.Run("rejects http endpoint", func(t *testing.T) {
		spec := ghav1alpha1.RedfishSpec{
			Endpoint: "http://127.0.0.1:8000",
		}
		_, err := NewGofishController(spec, "user", "pass", nil)
		if err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
			t.Fatalf("expected HTTPS error, got: %v", err)
		}
	})

	t.Run("accepts https endpoint", func(t *testing.T) {
		spec := ghav1alpha1.RedfishSpec{
			Endpoint: fakeBMC.URL(),
		}
		ctrl, err := NewGofishController(spec, "user", "pass", fakeBMC.CACertPEM())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ctrl == nil {
			t.Fatal("expected controller to be non-nil")
		}
	})

	t.Run("rejects invalid CA certificate PEM", func(t *testing.T) {
		spec := ghav1alpha1.RedfishSpec{
			Endpoint: "https://127.0.0.1:8000",
		}
		_, err := NewGofishController(spec, "user", "pass", []byte("invalid-ca-pem"))
		if err == nil || !strings.Contains(err.Error(), "invalid Redfish CA certificate") {
			t.Fatalf("expected invalid CA error, got: %v", err)
		}
	})
}

func TestGofishController_SystemResolution(t *testing.T) {
	fakeBMC := utils.NewFakeRedfishServer("Off")
	defer fakeBMC.Close()

	ctx := context.Background()

	t.Run("explicit systemID matched", func(t *testing.T) {
		spec := ghav1alpha1.RedfishSpec{
			Endpoint: fakeBMC.URL(),
			SystemID: "1",
			TLS: ghav1alpha1.RedfishTLSSpec{
				InsecureSkipVerify: true,
			},
		}
		ctrl, err := NewGofishController(spec, "user", "pass", nil)
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}
		state, err := ctrl.GetPowerState(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if state != ghav1alpha1.PowerStateOff {
			t.Fatalf("expected Off, got %v", state)
		}
	})

	t.Run("explicit systemID not found - fails closed", func(t *testing.T) {
		spec := ghav1alpha1.RedfishSpec{
			Endpoint: fakeBMC.URL(),
			SystemID: "non-existent-system",
			TLS: ghav1alpha1.RedfishTLSSpec{
				InsecureSkipVerify: true,
			},
		}
		ctrl, err := NewGofishController(spec, "user", "pass", nil)
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}
		_, err = ctrl.GetPowerState(ctx)
		if err == nil || !strings.Contains(err.Error(), "computer system \"non-existent-system\" not found") {
			t.Fatalf("expected fail-closed error, got: %v", err)
		}
	})

	t.Run("empty systemID with single system resolves to systems[0]", func(t *testing.T) {
		fakeBMC.SystemsCount = 1
		spec := ghav1alpha1.RedfishSpec{
			Endpoint: fakeBMC.URL(),
			SystemID: "",
			TLS: ghav1alpha1.RedfishTLSSpec{
				InsecureSkipVerify: true,
			},
		}
		ctrl, err := NewGofishController(spec, "user", "pass", nil)
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}
		state, err := ctrl.GetPowerState(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if state != ghav1alpha1.PowerStateOff {
			t.Fatalf("expected Off, got %v", state)
		}
	})

	t.Run("empty systemID with multiple systems errors", func(t *testing.T) {
		fakeBMC.SystemsCount = 2
		spec := ghav1alpha1.RedfishSpec{
			Endpoint: fakeBMC.URL(),
			SystemID: "",
			TLS: ghav1alpha1.RedfishTLSSpec{
				InsecureSkipVerify: true,
			},
		}
		ctrl, err := NewGofishController(spec, "user", "pass", nil)
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}
		_, err = ctrl.GetPowerState(ctx)
		if err == nil || !strings.Contains(err.Error(), "systemID is required") {
			t.Fatalf("expected error requiring systemID, got: %v", err)
		}
	})
}

func TestGofishController_PowerOperations(t *testing.T) {
	fakeBMC := utils.NewFakeRedfishServer("Off")
	defer fakeBMC.Close()

	ctx := context.Background()
	spec := ghav1alpha1.RedfishSpec{
		Endpoint: fakeBMC.URL(),
		SystemID: "1",
		TLS: ghav1alpha1.RedfishTLSSpec{
			InsecureSkipVerify: true,
		},
	}
	ctrl, err := NewGofishController(spec, "user", "pass", nil)
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	t.Run("PowerOn", func(t *testing.T) {
		fakeBMC.PowerState = "Off"
		if err := ctrl.PowerOn(ctx); err != nil {
			t.Fatalf("PowerOn failed: %v", err)
		}
		if fakeBMC.LastResetType != "On" {
			t.Fatalf("expected LastResetType 'On', got %q", fakeBMC.LastResetType)
		}
		if fakeBMC.PowerState != "On" {
			t.Fatalf("expected PowerState 'On', got %q", fakeBMC.PowerState)
		}
	})

	t.Run("PowerOn with ForceOn support only", func(t *testing.T) {
		fakeBMC.PowerState = "Off"
		fakeBMC.AllowableResetTypes = []string{"ForceOn", "ForceOff"}
		if err := ctrl.PowerOn(ctx); err != nil {
			t.Fatalf("PowerOn failed: %v", err)
		}
		if fakeBMC.LastResetType != "ForceOn" {
			t.Fatalf("expected LastResetType 'ForceOn', got %q", fakeBMC.LastResetType)
		}
	})

	t.Run("GracefulShutdown", func(t *testing.T) {
		fakeBMC.PowerState = "On"
		fakeBMC.AllowableResetTypes = []string{"On", "GracefulShutdown", "ForceOff"}
		if err := ctrl.GracefulShutdown(ctx); err != nil {
			t.Fatalf("GracefulShutdown failed: %v", err)
		}
		if fakeBMC.LastResetType != "GracefulShutdown" {
			t.Fatalf("expected LastResetType 'GracefulShutdown', got %q", fakeBMC.LastResetType)
		}
	})

	t.Run("GracefulShutdown fallback to PushPowerButton when Graceful not in AllowableValues", func(t *testing.T) {
		fakeBMC.PowerState = "On"
		fakeBMC.AllowableResetTypes = []string{"On", "PushPowerButton", "ForceOff"}
		if err := ctrl.GracefulShutdown(ctx); err != nil {
			t.Fatalf("GracefulShutdown failed: %v", err)
		}
		if fakeBMC.LastResetType != "PushPowerButton" {
			t.Fatalf("expected LastResetType 'PushPowerButton', got %q", fakeBMC.LastResetType)
		}
	})

	t.Run("ForceOff", func(t *testing.T) {
		fakeBMC.PowerState = "On"
		fakeBMC.AllowableResetTypes = []string{"On", "ForceOff"}
		if err := ctrl.ForceOff(ctx); err != nil {
			t.Fatalf("ForceOff failed: %v", err)
		}
		if fakeBMC.LastResetType != "ForceOff" {
			t.Fatalf("expected LastResetType 'ForceOff', got %q", fakeBMC.LastResetType)
		}
	})

	t.Run("Consecutive operations do not fail due to connection reuse", func(t *testing.T) {
		fakeBMC.PowerState = "Off"
		fakeBMC.AllowableResetTypes = []string{"On", "GracefulShutdown", "ForceOff"}
		if err := ctrl.PowerOn(ctx); err != nil {
			t.Fatalf("PowerOn failed: %v", err)
		}
		if _, err := ctrl.GetPowerState(ctx); err != nil {
			t.Fatalf("GetPowerState failed: %v", err)
		}
		if err := ctrl.GracefulShutdown(ctx); err != nil {
			t.Fatalf("GracefulShutdown failed: %v", err)
		}
	})
}

func TestGofishController_ValidateSupport(t *testing.T) {
	fakeBMC := utils.NewFakeRedfishServer("Off")
	defer fakeBMC.Close()

	ctx := context.Background()

	t.Run("valid capabilities", func(t *testing.T) {
		fakeBMC.AllowableResetTypes = []string{"On", "GracefulShutdown", "ForceOff"}
		spec := ghav1alpha1.RedfishSpec{
			Endpoint: fakeBMC.URL(),
			SystemID: "1",
			TLS: ghav1alpha1.RedfishTLSSpec{
				InsecureSkipVerify: true,
			},
		}
		ctrl, err := NewGofishController(spec, "user", "pass", nil)
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}
		if err := ctrl.ValidateSupport(ctx); err != nil {
			t.Fatalf("expected ValidateSupport to succeed, got: %v", err)
		}
	})

	t.Run("missing power on capability", func(t *testing.T) {
		fakeBMC.AllowableResetTypes = []string{"GracefulShutdown", "ForceOff"}
		spec := ghav1alpha1.RedfishSpec{
			Endpoint: fakeBMC.URL(),
			SystemID: "1",
			TLS: ghav1alpha1.RedfishTLSSpec{
				InsecureSkipVerify: true,
			},
		}
		ctrl, err := NewGofishController(spec, "user", "pass", nil)
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}
		err = ctrl.ValidateSupport(ctx)
		if err == nil || !strings.Contains(err.Error(), "does not support power on") {
			t.Fatalf("expected missing power on error, got: %v", err)
		}
	})

	t.Run("missing graceful shutdown capability", func(t *testing.T) {
		fakeBMC.AllowableResetTypes = []string{"On", "ForceOff"}
		spec := ghav1alpha1.RedfishSpec{
			Endpoint: fakeBMC.URL(),
			SystemID: "1",
			TLS: ghav1alpha1.RedfishTLSSpec{
				InsecureSkipVerify: true,
			},
		}
		ctrl, err := NewGofishController(spec, "user", "pass", nil)
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}
		err = ctrl.ValidateSupport(ctx)
		if err == nil || !strings.Contains(err.Error(), "does not support graceful shutdown") {
			t.Fatalf("expected missing graceful shutdown error, got: %v", err)
		}
	})

	t.Run("missing force off capability", func(t *testing.T) {
		fakeBMC.AllowableResetTypes = []string{"On", "GracefulShutdown"}
		spec := ghav1alpha1.RedfishSpec{
			Endpoint: fakeBMC.URL(),
			SystemID: "1",
			TLS: ghav1alpha1.RedfishTLSSpec{
				InsecureSkipVerify: true,
			},
		}
		ctrl, err := NewGofishController(spec, "user", "pass", nil)
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}
		err = ctrl.ValidateSupport(ctx)
		if err == nil || !strings.Contains(err.Error(), "does not support ForceOff") {
			t.Fatalf("expected missing ForceOff error, got: %v", err)
		}
	})
}

func TestGofishController_AllPowerStates(t *testing.T) {
	fakeBMC := utils.NewFakeRedfishServer("PoweringOn")
	defer fakeBMC.Close()

	ctx := context.Background()
	spec := ghav1alpha1.RedfishSpec{
		Endpoint: fakeBMC.URL(),
		SystemID: "1",
		TLS: ghav1alpha1.RedfishTLSSpec{
			InsecureSkipVerify: true,
		},
	}
	factory := NewGofishControllerFactory()
	ctrl, err := factory.NewController(spec, "user", "pass", nil)
	if err != nil {
		t.Fatalf("failed to create controller from factory: %v", err)
	}

	// 1. PoweringOn
	state, err := ctrl.GetPowerState(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != ghav1alpha1.PowerStatePoweringOn {
		t.Errorf("expected PoweringOn, got %v", state)
	}

	// 2. PoweringOff
	fakeBMC.PowerState = "PoweringOff"
	state, err = ctrl.GetPowerState(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != ghav1alpha1.PowerStatePoweringOff {
		t.Errorf("expected PoweringOff, got %v", state)
	}

	// 3. Paused
	fakeBMC.PowerState = "Paused"
	state, err = ctrl.GetPowerState(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != ghav1alpha1.PowerStateUnknown {
		t.Errorf("expected Unknown for Paused state, got %v", state)
	}

	// 4. Unknown
	fakeBMC.PowerState = "SomeUnknownState"
	state, err = ctrl.GetPowerState(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != ghav1alpha1.PowerStateUnknown {
		t.Errorf("expected Unknown, got %v", state)
	}
}
