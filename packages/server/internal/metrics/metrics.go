package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)
	HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)
	ActiveDevicesGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_devices",
			Help: "Number of currently online devices",
		},
	)
	RegisteredDevicesGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "registered_devices_total",
			Help: "Total number of registered devices",
		},
	)
	DBOpenConnsGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "db_open_connections",
			Help: "Number of open database connections",
		},
	)
	DBInUseConnsGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "db_in_use_connections",
			Help: "Number of in-use database connections",
		},
	)
	DBIdleConnsGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "db_idle_connections",
			Help: "Number of idle database connections",
		},
	)

	// Per-tenant gauges. Labelled "tenant_id" so we can alert on a single
	// tenant's noisy fleet without masking it under global aggregates.
	DevicesByTenant = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vaporrmm_tenant_devices",
			Help: "Number of registered devices per tenant",
		},
		[]string{"tenant_id"},
	)
	OnlineDevicesByTenant = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vaporrmm_tenant_devices_online",
			Help: "Number of online devices per tenant",
		},
		[]string{"tenant_id"},
	)
	UsersByTenant = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vaporrmm_tenant_users",
			Help: "Number of users per tenant",
		},
		[]string{"tenant_id"},
	)
	TenantsActive = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "vaporrmm_tenants_active",
			Help: "Number of tenants in active status",
		},
	)
	TenantsSuspended = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "vaporrmm_tenants_suspended",
			Help: "Number of tenants in suspended status",
		},
	)
)

func init() {
	register := func(c prometheus.Collector) {
		if err := prometheus.Register(c); err != nil {
			if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
				panic(err)
			}
		}
	}
	register(HTTPRequestsTotal)
	register(HTTPRequestDuration)
	register(ActiveDevicesGauge)
	register(RegisteredDevicesGauge)
	register(DBOpenConnsGauge)
	register(DBInUseConnsGauge)
	register(DBIdleConnsGauge)
	register(DevicesByTenant)
	register(OnlineDevicesByTenant)
	register(UsersByTenant)
	register(TenantsActive)
	register(TenantsSuspended)
}
