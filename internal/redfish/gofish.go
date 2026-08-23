package redfish

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/metrics"
)

type gofishControllerFactory struct{}

// NewGofishControllerFactory returns a PowerControllerFactory using gofish.
func NewGofishControllerFactory() PowerControllerFactory {
	return &gofishControllerFactory{}
}

func (f *gofishControllerFactory) NewController(spec ghav1alpha1.RedfishSpec, username, password string, caCert []byte) (PowerController, error) {
	return NewGofishController(spec, username, password, caCert)
}

type gofishController struct {
	spec     ghav1alpha1.RedfishSpec
	username string
	password string
	caCert   []byte
}

// NewGofishController creates a new PowerController backed by gofish.
func NewGofishController(spec ghav1alpha1.RedfishSpec, username, password string, caCert []byte) (PowerController, error) {
	return &gofishController{
		spec:     spec,
		username: username,
		password: password,
		caCert:   caCert,
	}, nil
}

func (c *gofishController) getClientConfig() gofish.ClientConfig {
	cfg := gofish.ClientConfig{
		Endpoint:         c.spec.Endpoint,
		Username:         c.username,
		Password:         c.password,
		Insecure:         c.spec.TLS.InsecureSkipVerify,
		BasicAuth:        true,
		ReuseConnections: true,
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: c.spec.TLS.InsecureSkipVerify,
	}

	if len(c.caCert) > 0 {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(c.caCert)
		tlsConfig.RootCAs = pool
	}

	tr := &http.Transport{
		TLSClientConfig: tlsConfig,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   5,
	}
	cfg.HTTPClient = &http.Client{
		Transport: tr,
		Timeout:   15 * time.Second,
	}

	return cfg
}

func (c *gofishController) withSystem(_ context.Context, fn func(sys *schemas.ComputerSystem) error) error {
	cfg := c.getClientConfig()
	client, err := gofish.Connect(cfg)
	if err != nil {
		return fmt.Errorf("failed to connect to Redfish endpoint: %w", err)
	}
	defer client.Logout()

	service := client.GetService()
	if service == nil {
		return fmt.Errorf("failed to get Redfish service root")
	}

	systems, err := service.Systems()
	if err != nil {
		return fmt.Errorf("failed to list systems from Redfish: %w", err)
	}

	if len(systems) == 0 {
		return fmt.Errorf("no computer systems found on Redfish endpoint")
	}

	systemID := c.spec.SystemID
	if systemID == "" {
		systemID = "1"
	}

	var targetSystem *schemas.ComputerSystem
	for _, sys := range systems {
		if sys.ID == systemID || sys.Name == systemID {
			targetSystem = sys
			break
		}
	}

	if targetSystem == nil {
		// Fallback to the first system if matching fails
		targetSystem = systems[0]
	}

	return fn(targetSystem)
}

func recordRedfishMetric(operation string, startTime time.Time, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	metrics.RedfishRequestsTotal.WithLabelValues("global", "redfish", operation, result).Inc()
	metrics.RedfishRequestDuration.WithLabelValues("global", "redfish", operation).Observe(time.Since(startTime).Seconds())
}

func (c *gofishController) GetPowerState(ctx context.Context) (ghav1alpha1.PowerState, error) {
	start := time.Now()
	var state ghav1alpha1.PowerState
	err := c.withSystem(ctx, func(sys *schemas.ComputerSystem) error {
		switch sys.PowerState {
		case schemas.OnPowerState:
			state = ghav1alpha1.PowerStateOn
		case schemas.OffPowerState:
			state = ghav1alpha1.PowerStateOff
		case schemas.PoweringOnPowerState:
			state = ghav1alpha1.PowerStatePoweringOn
		case schemas.PoweringOffPowerState:
			state = ghav1alpha1.PowerStatePoweringOff
		case schemas.PausedPowerState:
			state = ghav1alpha1.PowerStateUnknown
		default:
			state = ghav1alpha1.PowerStateUnknown
		}
		return nil
	})
	recordRedfishMetric("get_power_state", start, err)
	if err != nil {
		return ghav1alpha1.PowerStateUnknown, err
	}
	return state, nil
}

func (c *gofishController) PowerOn(ctx context.Context) error {
	start := time.Now()
	err := c.withSystem(ctx, func(sys *schemas.ComputerSystem) error {
		if sys.PowerState == schemas.OnPowerState || sys.PowerState == schemas.PoweringOnPowerState {
			return nil
		}
		_, err := sys.Reset(schemas.OnResetType)
		if err != nil {
			// Observe-Act-Observe: エラーでも現在の状態を再確認
			_ = sys.Update()
			if sys.PowerState == schemas.OnPowerState || sys.PowerState == schemas.PoweringOnPowerState {
				return nil
			}
			return err
		}
		return nil
	})
	recordRedfishMetric("power_on", start, err)
	return err
}

func (c *gofishController) GracefulShutdown(ctx context.Context) error {
	start := time.Now()
	err := c.withSystem(ctx, func(sys *schemas.ComputerSystem) error {
		if sys.PowerState == schemas.OffPowerState || sys.PowerState == schemas.PoweringOffPowerState {
			return nil
		}

		// SupportedResetTypes (AllowableValues) を確認して最適なResetTypeを選択
		supportedTypes, _ := sys.GetSupportedResetTypes()
		hasGraceful := false
		hasPushButton := false
		for _, rt := range supportedTypes {
			if rt == schemas.GracefulShutdownResetType {
				hasGraceful = true
			}
			if rt == schemas.PushPowerButtonResetType {
				hasPushButton = true
			}
		}

		if hasGraceful || len(supportedTypes) == 0 {
			_, err := sys.Reset(schemas.GracefulShutdownResetType)
			if err == nil {
				return nil
			}
			if isUnsupportedResetError(err) && (hasPushButton || len(supportedTypes) == 0) {
				_, pushErr := sys.Reset(schemas.PushPowerButtonResetType)
				if pushErr == nil {
					return nil
				}
				return fmt.Errorf("GracefulShutdown failed (%v), PushPowerButton also failed: %w", err, pushErr)
			}
			return err
		}

		if hasPushButton {
			_, err := sys.Reset(schemas.PushPowerButtonResetType)
			return err
		}

		return fmt.Errorf("neither GracefulShutdown nor PushPowerButton supported by BMC")
	})
	recordRedfishMetric("graceful_shutdown", start, err)
	return err
}

func (c *gofishController) ForceOff(ctx context.Context) error {
	start := time.Now()
	err := c.withSystem(ctx, func(sys *schemas.ComputerSystem) error {
		if sys.PowerState == schemas.OffPowerState {
			return nil
		}
		_, err := sys.Reset(schemas.ForceOffResetType)
		return err
	})
	recordRedfishMetric("force_off", start, err)
	return err
}

func (c *gofishController) ValidateSupport(_ context.Context) error {
	cfg := c.getClientConfig()
	client, err := gofish.Connect(cfg)
	if err != nil {
		return fmt.Errorf("failed to connect to Redfish endpoint: %w", err)
	}
	defer client.Logout()

	service := client.GetService()
	if service == nil {
		return fmt.Errorf("service root not reachable")
	}

	systems, err := service.Systems()
	if err != nil || len(systems) == 0 {
		return fmt.Errorf("failed to retrieve systems: %w", err)
	}

	return nil
}

func isUnsupportedResetError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "unsupported") ||
		strings.Contains(msg, "invalid") ||
		strings.Contains(msg, "400")
}
