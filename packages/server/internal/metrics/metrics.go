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
}
