package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/toska-mesh/toska-mesh/internal/consul"
)

// DashboardProxy proxies requests to internal observability services
// (Prometheus, Tracing, HealthMonitor) and serves the service catalog
// directly from Consul.
type DashboardProxy struct {
	config   DashboardConfig
	logger   *slog.Logger
	client   *http.Client
	registry *consul.Registry
}

// NewDashboardProxy creates a proxy for dashboard API routes.
func NewDashboardProxy(config DashboardConfig, registry *consul.Registry, logger *slog.Logger) *DashboardProxy {
	return &DashboardProxy{
		config:   config,
		logger:   logger,
		client:   &http.Client{Timeout: 10 * time.Second},
		registry: registry,
	}
}

// Handler returns an http.Handler mounted at /api/dashboard/.
func (dp *DashboardProxy) Handler() http.Handler {
	mux := http.NewServeMux()

	// Prometheus proxy.
	mux.HandleFunc("/api/dashboard/prometheus/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/dashboard/prometheus")
		dp.proxy(w, r, dp.config.PrometheusBaseURL, "/api/v1"+path)
	})

	// Tracing proxy (exact path for list endpoint + subtree for sub-paths).
	tracingHandler := func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/dashboard/traces")
		dp.proxy(w, r, dp.config.TracingBaseURL, "/api/traces"+path)
	}
	mux.HandleFunc("/api/dashboard/traces", tracingHandler)
	mux.HandleFunc("/api/dashboard/traces/", tracingHandler)

	// Services catalog (direct from Consul).
	mux.HandleFunc("/api/dashboard/services", dp.handleServices)

	// Health snapshots (via HealthMonitor).
	mux.HandleFunc("/api/dashboard/health", func(w http.ResponseWriter, r *http.Request) {
		dp.proxy(w, r, dp.config.HealthMonitorBaseURL, "/api/status")
	})

	return mux
}

// serviceCatalogItem matches DashboardServiceCatalogItem in the dashboard frontend.
type serviceCatalogItem struct {
	ServiceName string                  `json:"serviceName"`
	Instances   []serviceInstance       `json:"instances"`
	Health      []any                   `json:"health"`
	Metadata    *serviceMetadataSummary `json:"metadata"`
}

type serviceInstance struct {
	ServiceName    string            `json:"serviceName"`
	ServiceID      string            `json:"serviceId"`
	Address        string            `json:"address"`
	Port           int               `json:"port"`
	Status         string            `json:"status"`
	Metadata       map[string]string `json:"metadata"`
	RegisteredAt   string            `json:"registeredAt"`
	LastHealthCheck string           `json:"lastHealthCheck"`
}

type serviceMetadataSummary struct {
	ServiceName   string               `json:"serviceName"`
	InstanceCount int                  `json:"instanceCount"`
	GeneratedAt   string               `json:"generatedAt"`
	Keys          []metadataKeySummary `json:"keys"`
}

type metadataKeySummary struct {
	Key           string   `json:"key"`
	InstanceCount int      `json:"instanceCount"`
	Values        []string `json:"values"`
}

func (dp *DashboardProxy) handleServices(w http.ResponseWriter, r *http.Request) {
	serviceNames, err := dp.registry.GetServices()
	if err != nil {
		dp.logger.Warn("failed to list services from consul", "error", err)
		http.Error(w, "failed to query consul", http.StatusBadGateway)
		return
	}

	sort.Strings(serviceNames)
	now := time.Now().UTC().Format(time.RFC3339)

	catalog := make([]serviceCatalogItem, 0, len(serviceNames))
	for _, name := range serviceNames {
		instances, err := dp.registry.GetInstances(name)
		if err != nil {
			dp.logger.Warn("failed to get instances", "service", name, "error", err)
			continue
		}

		items := make([]serviceInstance, 0, len(instances))
		for _, inst := range instances {
			regAt := ""
			if !inst.RegisteredAt.IsZero() {
				regAt = inst.RegisteredAt.Format(time.RFC3339)
			}
			lastHC := ""
			if !inst.LastHealthCheck.IsZero() {
				lastHC = inst.LastHealthCheck.Format(time.RFC3339)
			}
			items = append(items, serviceInstance{
				ServiceName:     inst.ServiceName,
				ServiceID:       inst.ServiceID,
				Address:         inst.Address,
				Port:            inst.Port,
				Status:          inst.Status.String(),
				Metadata:        inst.Metadata,
				RegisteredAt:    regAt,
				LastHealthCheck: lastHC,
			})
		}

		// Build metadata summary from instance metadata keys.
		var meta *serviceMetadataSummary
		if len(items) > 0 {
			keyMap := make(map[string]map[string]struct{})
			for _, inst := range instances {
				for k, v := range inst.Metadata {
					if keyMap[k] == nil {
						keyMap[k] = make(map[string]struct{})
					}
					keyMap[k][v] = struct{}{}
				}
			}
			keys := make([]metadataKeySummary, 0, len(keyMap))
			for k, vals := range keyMap {
				vs := make([]string, 0, len(vals))
				for v := range vals {
					vs = append(vs, v)
				}
				sort.Strings(vs)
				keys = append(keys, metadataKeySummary{
					Key:           k,
					InstanceCount: len(vals),
					Values:        vs,
				})
			}
			sort.Slice(keys, func(i, j int) bool { return keys[i].Key < keys[j].Key })
			meta = &serviceMetadataSummary{
				ServiceName:   name,
				InstanceCount: len(items),
				GeneratedAt:   now,
				Keys:          keys,
			}
		}

		catalog = append(catalog, serviceCatalogItem{
			ServiceName: name,
			Instances:   items,
			Health:      []any{},
			Metadata:    meta,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(catalog)
}

func (dp *DashboardProxy) proxy(w http.ResponseWriter, r *http.Request, baseURL, path string) {
	targetURL := baseURL + path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Forward relevant headers.
	for _, h := range []string{"Content-Type", "Accept"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}

	// Inject service-to-service auth token for C# backend services.
	if dp.config.ServiceAuthSecret != "" {
		token := dp.signServiceToken()
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := dp.client.Do(req)
	if err != nil {
		dp.logger.Warn("dashboard proxy failed", "url", targetURL, "error", err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// signServiceToken generates a short-lived HS256 JWT compatible with C# MeshServiceAuth.
func (dp *DashboardProxy) signServiceToken() string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

	now := time.Now().UTC()
	claims := map[string]any{
		"sub":                                       "gateway",
		"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/nameidentifier": "gateway",
		"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name":           "gateway",
		"service_id": "gateway",
		"http://schemas.microsoft.com/ws/2008/06/identity/claims/role": "Service",
		"jti": now.Format("20060102150405"),
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": "ToskaMesh.Services",
		"aud": "ToskaMesh.Services",
	}
	payloadJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)

	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, []byte(dp.config.ServiceAuthSecret))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + sig
}
