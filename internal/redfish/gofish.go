package redfish

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/metrics"
	"golang.org/x/time/rate"
)

type endpointGate struct {
	concurrency chan struct{}
	limiter     *rate.Limiter
}

var (
	globalRedfishSemaphore = make(chan struct{}, 8)
	endpointGatesMu        sync.Mutex
	endpointGates          = make(map[string]*endpointGate)
)

func getEndpointGate(rawEndpoint string) *endpointGate {
	endpointKey := rawEndpoint
	if parsed, err := url.Parse(rawEndpoint); err == nil && parsed.Host != "" {
		endpointKey = fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	}

	endpointGatesMu.Lock()
	defer endpointGatesMu.Unlock()

	gate, exists := endpointGates[endpointKey]
	if !exists {
		gate = &endpointGate{
			concurrency: make(chan struct{}, 1),
			limiter:     rate.NewLimiter(rate.Limit(2), 2), // 2 operations/sec, burst 2
		}
		endpointGates[endpointKey] = gate
	}
	return gate
}

type gofishControllerFactory struct{}

// NewGofishControllerFactory returns a PowerControllerFactory using gofish.
func NewGofishControllerFactory() PowerControllerFactory {
	return &gofishControllerFactory{}
}

func (f *gofishControllerFactory) NewController(spec ghav1alpha1.RedfishSpec, username, password string, caCert []byte) (PowerController, error) {
	return NewGofishController(spec, username, password, caCert)
}

type gofishController struct {
	spec       ghav1alpha1.RedfishSpec
	username   string
	password   string
	caCert     []byte
	clientMu   sync.Mutex
	baseClient *gofish.APIClient
}

// NewGofishController creates a new PowerController backed by gofish.
func NewGofishController(spec ghav1alpha1.RedfishSpec, username, password string, caCert []byte) (PowerController, error) {
	u, err := url.Parse(spec.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid Redfish endpoint: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("redfish endpoint must use HTTPS (got %q)", spec.Endpoint)
	}

	if len(caCert) > 0 {
		pool := x509.NewCertPool()
		if ok := pool.AppendCertsFromPEM(caCert); !ok {
			return nil, fmt.Errorf("invalid Redfish CA certificate: unable to parse PEM")
		}
	}

	return &gofishController{
		spec:     spec,
		username: username,
		password: password,
		caCert:   caCert,
	}, nil
}

func (c *gofishController) getClientConfig() gofish.ClientConfig {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: c.spec.TLS.InsecureSkipVerify,
	}

	if len(c.caCert) > 0 {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(c.caCert)
		tlsConfig.RootCAs = pool
	}

	tr := &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: tlsConfig,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		DisableKeepAlives:     true,
	}

	httpClient := &http.Client{
		Transport: tr,
		Timeout:   15 * time.Second,
	}

	return gofish.ClientConfig{
		Endpoint:              c.spec.Endpoint,
		Username:              c.username,
		Password:              c.password,
		Insecure:              c.spec.TLS.InsecureSkipVerify,
		BasicAuth:             true,
		ReuseConnections:      false,
		MaxConcurrentRequests: 1,
		NoModifyTransport:     true,
		HTTPClient:            httpClient,
	}
}

func (c *gofishController) invalidateClient() {
	c.clientMu.Lock()
	defer c.clientMu.Unlock()

	if c.baseClient != nil {
		if c.baseClient.HTTPClient != nil {
			c.baseClient.HTTPClient.CloseIdleConnections()
		}
		c.baseClient = nil
	}
}

func (c *gofishController) getOrCreateClient(ctx context.Context) (*gofish.APIClient, error) {
	c.clientMu.Lock()
	defer c.clientMu.Unlock()

	if c.baseClient != nil {
		return c.baseClient, nil
	}

	cfg := c.getClientConfig()
	client, err := gofish.ConnectContext(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redfish endpoint: %w", err)
	}

	c.baseClient = client
	return c.baseClient, nil
}

func resolveSystem(systems []*schemas.ComputerSystem, systemID string) (*schemas.ComputerSystem, error) {
	if systemID == "" {
		if len(systems) != 1 {
			return nil, fmt.Errorf("systemID is required: Redfish endpoint exposes %d computer systems", len(systems))
		}
		return systems[0], nil
	}

	for _, sys := range systems {
		if sys.ID == systemID {
			return sys, nil
		}
	}

	return nil, fmt.Errorf("computer system %q not found", systemID)
}

func (c *gofishController) withSystem(ctx context.Context, fn func(sys *schemas.ComputerSystem) error) error {
	gate := getEndpointGate(c.spec.Endpoint)

	// 1. Endpoint Concurrency (1 operation per physical BMC host)
	select {
	case gate.concurrency <- struct{}{}:
		defer func() { <-gate.concurrency }()
	case <-ctx.Done():
		return ctx.Err()
	}

	// 2. Endpoint Rate Limiter
	if err := gate.limiter.Wait(ctx); err != nil {
		return err
	}

	// 3. Global Semaphore
	select {
	case globalRedfishSemaphore <- struct{}{}:
		defer func() { <-globalRedfishSemaphore }()
	case <-ctx.Done():
		return ctx.Err()
	}

	execute := func() error {
		base, err := c.getOrCreateClient(ctx)
		if err != nil {
			return err
		}

		// Bind request Context
		client := base.WithContext(ctx)

		service := client.GetService()
		if service == nil {
			c.invalidateClient()
			return fmt.Errorf("failed to get Redfish service root")
		}

		systems, err := service.Systems()
		if err != nil {
			c.invalidateClient()
			return fmt.Errorf("failed to list systems from Redfish: %w", err)
		}

		if len(systems) == 0 {
			return fmt.Errorf("redfish endpoint exposes no computer systems")
		}

		targetSystem, err := resolveSystem(systems, c.spec.SystemID)
		if err != nil {
			return err
		}

		if fnErr := fn(targetSystem); fnErr != nil {
			c.invalidateClient()
			return fnErr
		}
		return nil
	}

	err := execute()
	if err != nil && isNetworkOrEOFError(err) {
		// アイドル切断やコネクション断の場合、1回だけ新規コネクションで再試行
		c.invalidateClient()
		err = execute()
	}
	return err
}

func recordRedfishMetric(ctx context.Context, operation string, startTime time.Time, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	metrics.RedfishRequestsTotal.WithLabelValues("global", "redfish", operation, result).Inc(ctx)
	metrics.RedfishRequestDuration.WithLabelValues("global", "redfish", operation).Observe(ctx, time.Since(startTime).Seconds())
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
	recordRedfishMetric(ctx, "get_power_state", start, err)
	if err != nil {
		return ghav1alpha1.PowerStateUnknown, err
	}
	return state, nil
}

type redfishActionReset struct {
	AllowableValues []string `json:"ResetType@Redfish.AllowableValues"`
}

type redfishAvailableAction struct {
	Action       string `json:"Action"`
	Capabilities []struct {
		AllowableValues []string `json:"AllowableValues"`
		PropertyName    string   `json:"PropertyName"`
	} `json:"Capabilities"`
}

type redfishRawSystem struct {
	Actions          map[string]json.RawMessage `json:"Actions"`
	AvailableActions []redfishAvailableAction   `json:"AvailableActions"`
}

func getSupportedResetTypesFromSystem(sys *schemas.ComputerSystem) []schemas.ResetType {
	types, err := sys.GetSupportedResetTypes()
	if err == nil && len(types) > 0 {
		return types
	}

	if len(sys.RawData) == 0 {
		return nil
	}

	var raw redfishRawSystem
	if err := json.Unmarshal(sys.RawData, &raw); err != nil {
		return nil
	}

	var allowable []string
	for k, v := range raw.Actions {
		if strings.Contains(k, "ComputerSystem.Reset") {
			var actionReset redfishActionReset
			if err := json.Unmarshal(v, &actionReset); err == nil && len(actionReset.AllowableValues) > 0 {
				allowable = append(allowable, actionReset.AllowableValues...)
			}
		}
	}

	for _, aa := range raw.AvailableActions {
		if strings.EqualFold(aa.Action, "Reset") {
			for _, cap := range aa.Capabilities {
				if strings.EqualFold(cap.PropertyName, "ResetType") && len(cap.AllowableValues) > 0 {
					allowable = append(allowable, cap.AllowableValues...)
				}
			}
		}
	}

	if len(allowable) == 0 {
		return nil
	}

	seen := make(map[schemas.ResetType]bool)
	var result []schemas.ResetType
	for _, a := range allowable {
		rt := schemas.ResetType(a)
		if !seen[rt] {
			seen[rt] = true
			result = append(result, rt)
		}
	}
	return result
}

func (c *gofishController) PowerOn(ctx context.Context) error {
	start := time.Now()
	err := c.withSystem(ctx, func(sys *schemas.ComputerSystem) error {
		if sys.PowerState == schemas.OnPowerState || sys.PowerState == schemas.PoweringOnPowerState {
			return nil
		}

		supportedTypes := getSupportedResetTypesFromSystem(sys)

		targetResetType := schemas.OnResetType
		if len(supportedTypes) > 0 {
			hasOn := false
			hasForceOn := false
			for _, rt := range supportedTypes {
				if rt == schemas.OnResetType {
					hasOn = true
				}
				if rt == schemas.ForceOnResetType {
					hasForceOn = true
				}
			}
			if hasOn {
				targetResetType = schemas.OnResetType
			} else if hasForceOn {
				targetResetType = schemas.ForceOnResetType
			} else {
				return fmt.Errorf("neither On nor ForceOn supported by BMC (supported: %v)", supportedTypes)
			}
		}

		_, err := sys.Reset(targetResetType)
		if err != nil {
			// If target was On and it returned unsupported error when supportedTypes was unknown, try ForceOn
			if len(supportedTypes) == 0 && targetResetType == schemas.OnResetType && isUnsupportedResetError(err) {
				if _, forceErr := sys.Reset(schemas.ForceOnResetType); forceErr == nil {
					return nil
				}
			}

			// Observe-Act-Observe: エラーでも現在の状態を再確認
			if updateErr := sys.Update(); updateErr == nil && (sys.PowerState == schemas.OnPowerState || sys.PowerState == schemas.PoweringOnPowerState) {
				return nil
			}
			return err
		}
		return nil
	})
	recordRedfishMetric(ctx, "power_on", start, err)
	return err
}

func (c *gofishController) GracefulShutdown(ctx context.Context) error {
	start := time.Now()
	err := c.withSystem(ctx, func(sys *schemas.ComputerSystem) error {
		if sys.PowerState == schemas.OffPowerState || sys.PowerState == schemas.PoweringOffPowerState {
			return nil
		}

		// SupportedResetTypes (AllowableValues) を確認して最適なResetTypeを選択
		supportedTypes := getSupportedResetTypesFromSystem(sys)
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

		if len(supportedTypes) > 0 {
			if hasGraceful {
				_, err := sys.Reset(schemas.GracefulShutdownResetType)
				if err == nil {
					return nil
				}
				if hasPushButton {
					if _, pushErr := sys.Reset(schemas.PushPowerButtonResetType); pushErr == nil {
						return nil
					}
				}
				return err
			}
			if hasPushButton {
				_, err := sys.Reset(schemas.PushPowerButtonResetType)
				return err
			}
			return fmt.Errorf("neither GracefulShutdown nor PushPowerButton supported by BMC (supported: %v)", supportedTypes)
		}

		// When supportedTypes is unknown, try GracefulShutdown and fallback to PushPowerButton
		_, err := sys.Reset(schemas.GracefulShutdownResetType)
		if err == nil {
			return nil
		}
		if isUnsupportedResetError(err) || isNetworkOrEOFError(err) {
			_, pushErr := sys.Reset(schemas.PushPowerButtonResetType)
			if pushErr == nil {
				return nil
			}
			return fmt.Errorf("GracefulShutdown failed (%v), PushPowerButton also failed: %w", err, pushErr)
		}
		return err
	})
	recordRedfishMetric(ctx, "graceful_shutdown", start, err)
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
	recordRedfishMetric(ctx, "force_off", start, err)
	return err
}

func (c *gofishController) ValidateSupport(ctx context.Context) error {
	start := time.Now()
	err := c.withSystem(ctx, func(sys *schemas.ComputerSystem) error {
		supportedTypes := getSupportedResetTypesFromSystem(sys)
		if len(supportedTypes) == 0 {
			// If BMC doesn't advertise allowable values via ActionInfo/AllowableValues,
			// having reached the ComputerSystem resource successfully is sufficient.
			return nil
		}

		hasPowerOn := false
		hasGracefulShutdown := false
		hasForceOff := false
		for _, rt := range supportedTypes {
			if rt == schemas.OnResetType || rt == schemas.ForceOnResetType {
				hasPowerOn = true
			}
			if rt == schemas.GracefulShutdownResetType || rt == schemas.PushPowerButtonResetType {
				hasGracefulShutdown = true
			}
			if rt == schemas.ForceOffResetType {
				hasForceOff = true
			}
		}

		if !hasPowerOn {
			return fmt.Errorf("computer system %q does not support power on (neither On nor ForceOn in allowable reset types: %v)", sys.ID, supportedTypes)
		}
		if !hasGracefulShutdown {
			return fmt.Errorf("computer system %q does not support graceful shutdown (neither GracefulShutdown nor PushPowerButton in allowable reset types: %v)", sys.ID, supportedTypes)
		}
		if !hasForceOff {
			return fmt.Errorf("computer system %q does not support ForceOff in allowable reset types: %v", sys.ID, supportedTypes)
		}

		return nil
	})
	recordRedfishMetric(ctx, "validate_support", start, err)
	return err
}

func isUnsupportedResetError(err error) bool {
	if err == nil {
		return false
	}

	if redfishErr, ok := errors.AsType[*schemas.Error](err); ok {
		code := strings.ToLower(redfishErr.Code)
		if strings.Contains(code, "actionparameternotsupported") ||
			strings.Contains(code, "actionnotsupported") ||
			strings.Contains(code, "propertyvaluenotinlist") ||
			strings.Contains(code, "notimplemented") {
			return true
		}
		for _, ext := range redfishErr.ExtendedInfos {
			msgID := strings.ToLower(ext.MessageID)
			if strings.Contains(msgID, "actionparameternotsupported") ||
				strings.Contains(msgID, "actionnotsupported") ||
				strings.Contains(msgID, "propertyvaluenotinlist") ||
				strings.Contains(msgID, "notimplemented") {
				return true
			}
		}
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "unsupported") ||
		strings.Contains(msg, "not implemented") ||
		strings.Contains(msg, "actionparameternotsupported") ||
		strings.Contains(msg, "actionnotsupported") ||
		strings.Contains(msg, "propertyvaluenotinlist")
}

func isNetworkOrEOFError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "eof") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "no route to host") ||
		strings.Contains(msg, "network is unreachable")
}
