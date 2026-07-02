package baseapp

// Gonka: comprehensive Prometheus metrics and slow-query log for state queries.
// These complement the existing go-metrics counters (`query_count`,
// `query_<method>`) and provide labelled histograms suitable for
// cross-node aggregation in Prometheus.
//
// The interceptor in grpcserver.go and the ABCI Query path in abci.go
// both record into these vectors; transport label distinguishes them.
//
// Tag with // Gonka: so future SDK rebases don't silently drop them.

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/cosmos/cosmos-sdk/telemetry"
)

const (
	gonkaQueryNamespace = "gonka"
	gonkaQuerySubsystem = "query"

	gonkaSlowQueryEnabledEnv         = "GONKA_SLOW_QUERY_ENABLED"
	gonkaSlowQueryThresholdMSEnv     = "GONKA_SLOW_QUERY_THRESHOLD_MS"
	gonkaSlowQueryRateLimitEnv       = "GONKA_SLOW_QUERY_RATE_LIMIT"
	gonkaSlowQueryRequestContentEnv  = "GONKA_SLOW_QUERY_REQUEST_CONTENT"
	gonkaSlowQueryRequestMaxBytesEnv = "GONKA_SLOW_QUERY_REQUEST_MAX_BYTES"

	GonkaTransportGRPC = "grpc"
	GonkaTransportREST = "rest"
	GonkaTransportABCI = "abci"

	GonkaPeerABCI     = "abci-internal"
	GonkaPeerUnknown  = "unknown"
	GonkaPeerOverflow = "overflow"

	gonkaPeerCardinalityCap = 256
)

var (
	gonkaQueryTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: gonkaQueryNamespace,
		Subsystem: gonkaQuerySubsystem,
		Name:      "total",
		Help:      "Total number of state queries by method, status, and transport.",
	}, []string{"method", "status", "transport"})

	gonkaQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: gonkaQueryNamespace,
		Subsystem: gonkaQuerySubsystem,
		Name:      "duration_seconds",
		Help:      "End-to-end latency of state queries in seconds (setup + handler).",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"method", "status", "transport"})

	gonkaQuerySetupDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: gonkaQueryNamespace,
		Subsystem: gonkaQuerySubsystem,
		Name:      "setup_duration_seconds",
		Help:      "Time spent constructing the query context (commit-phase blocking shows here).",
		Buckets:   []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 5},
	}, []string{"method", "transport"})

	gonkaQueryHandlerDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: gonkaQueryNamespace,
		Subsystem: gonkaQuerySubsystem,
		Name:      "handler_duration_seconds",
		Help:      "Time spent inside the query handler proper (state work).",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"method", "status", "transport"})

	gonkaQueryResponseBytes = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: gonkaQueryNamespace,
		Subsystem: gonkaQuerySubsystem,
		Name:      "response_bytes",
		Help:      "Response size distribution of state queries in bytes (64 B -> 64 MB).",
		Buckets:   prometheus.ExponentialBuckets(64, 4, 11),
	}, []string{"method", "status", "transport"})

	gonkaQueryInFlight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: gonkaQueryNamespace,
		Subsystem: gonkaQuerySubsystem,
		Name:      "in_flight",
		Help:      "Number of currently in-flight state queries.",
	}, []string{"method", "transport"})

	gonkaQueryGasUsed = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: gonkaQueryNamespace,
		Subsystem: gonkaQuerySubsystem,
		Name:      "gas_used",
		Help:      "Gas consumed by query handlers (internal SDK accounting; not billed). 1k -> 5G.",
		Buckets:   []float64{1e3, 1e4, 5e4, 1e5, 5e5, 1e6, 5e6, 1e7, 5e7, 1e8, 5e8, 1e9, 5e9},
	}, []string{"method", "status", "transport"})

	gonkaQueryHeightLag = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: gonkaQueryNamespace,
		Subsystem: gonkaQuerySubsystem,
		Name:      "height_lag_blocks",
		Help:      "Difference between current chain height and queried height. 0 = latest.",
		Buckets:   []float64{0, 1, 10, 100, 1000, 10000, 100000, 1000000},
	}, []string{"method"})

	gonkaQueryByPeer = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: gonkaQueryNamespace,
		Subsystem: gonkaQuerySubsystem,
		Name:      "by_peer_total",
		Help:      "Query rate by peer subnet. IPs are bucketed (IPv4 /24, IPv6 /48); cardinality capped at " + strconv.Itoa(gonkaPeerCardinalityCap) + ".",
	}, []string{"peer", "transport"})

	gonkaQuerySlow = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: gonkaQueryNamespace,
		Subsystem: gonkaQuerySubsystem,
		Name:      "slow_total",
		Help:      "Number of queries that exceeded the slow-query threshold.",
	}, []string{"method", "transport"})
)

// gonkaPeerSet bounds the cardinality of the peer label. Once the cap is hit,
// further new peers collapse into GonkaPeerOverflow. This is a one-shot bound;
// no eviction. Restart the node to reset.
var (
	gonkaPeerMu  sync.RWMutex
	gonkaPeerSet = make(map[string]struct{}, gonkaPeerCardinalityCap)
)

// gonkaSanitizePeer extracts the bucketed network from a "host:port" peer
// string and applies the cardinality cap. IPv4 -> /24, IPv6 -> /48.
func gonkaSanitizePeer(addr string) string {
	if addr == "" {
		return GonkaPeerUnknown
	}
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return GonkaPeerUnknown
	}
	var bucket string
	if ip4 := ip.To4(); ip4 != nil {
		bucket = fmt.Sprintf("%d.%d.%d.0/24", ip4[0], ip4[1], ip4[2])
	} else {
		mask := net.CIDRMask(48, 128)
		bucket = (&net.IPNet{IP: ip.Mask(mask), Mask: mask}).String()
	}

	gonkaPeerMu.RLock()
	_, ok := gonkaPeerSet[bucket]
	gonkaPeerMu.RUnlock()
	if ok {
		return bucket
	}

	gonkaPeerMu.Lock()
	defer gonkaPeerMu.Unlock()
	if _, ok := gonkaPeerSet[bucket]; ok {
		return bucket
	}
	if len(gonkaPeerSet) >= gonkaPeerCardinalityCap {
		return GonkaPeerOverflow
	}
	gonkaPeerSet[bucket] = struct{}{}
	return bucket
}

// gonkaClassifyGRPCErr maps a gRPC handler error to a stable status label.
func gonkaClassifyGRPCErr(err error) string {
	if err == nil {
		return "ok"
	}
	if s, ok := grpcstatus.FromError(err); ok {
		return s.Code().String()
	}
	return grpccodes.Unknown.String()
}

// gonkaClassifyABCICode maps an ABCI ResponseQuery code to a status label.
func gonkaClassifyABCICode(code uint32) string {
	if code == 0 {
		return "ok"
	}
	return fmt.Sprintf("code_%d", code)
}

// gonkaQueryRecord holds the fields needed to record query metrics and
// optionally emit a slow-query log line.
type gonkaQueryRecord struct {
	method          string
	transport       string
	status          string
	peer            string
	start           time.Time
	setupDuration   time.Duration
	handlerDuration time.Duration
	totalDuration   time.Duration
	respBytes       int
	gasConsumed     uint64
	requestedHeight int64
	currentHeight   int64
	requestSummary  func(includeContent bool) string
}

// gonkaObserve records all query Prometheus metrics in one place.
func gonkaObserve(r gonkaQueryRecord) {
	if r.totalDuration == 0 {
		r.totalDuration = time.Since(r.start)
	}

	gonkaQueryTotal.WithLabelValues(r.method, r.status, r.transport).Inc()
	gonkaQueryDuration.WithLabelValues(r.method, r.status, r.transport).Observe(r.totalDuration.Seconds())
	if r.setupDuration > 0 {
		gonkaQuerySetupDuration.WithLabelValues(r.method, r.transport).Observe(r.setupDuration.Seconds())
	}
	if r.handlerDuration > 0 {
		gonkaQueryHandlerDuration.WithLabelValues(r.method, r.status, r.transport).Observe(r.handlerDuration.Seconds())
	}
	if r.respBytes >= 0 {
		gonkaQueryResponseBytes.WithLabelValues(r.method, r.status, r.transport).Observe(float64(r.respBytes))
	}
	if r.gasConsumed > 0 {
		gonkaQueryGasUsed.WithLabelValues(r.method, r.status, r.transport).Observe(float64(r.gasConsumed))
	}
	lag := int64(0)
	if r.requestedHeight > 0 && r.currentHeight > r.requestedHeight {
		lag = r.currentHeight - r.requestedHeight
	}
	gonkaQueryHeightLag.WithLabelValues(r.method).Observe(float64(lag))
	if r.peer != "" {
		gonkaQueryByPeer.WithLabelValues(r.peer, r.transport).Inc()
	}
}

// gonkaTruncate keeps log lines compact.
func gonkaTruncate(s string, max int64) string {
	if max <= 0 || int64(len(s)) <= max {
		return s
	}
	return s[:int(max)] + "...(truncated)"
}

var (
	gonkaSlowLogLastTime time.Time
	gonkaSlowLogCount    int64
	gonkaSlowLogMu       sync.Mutex
)

// gonkaAllowSlowLog rate limits slow query warnings to prevent log/disk exhaustion.
func gonkaAllowSlowLog(limit int64) bool {
	if limit == 0 {
		return true
	}

	gonkaSlowLogMu.Lock()
	defer gonkaSlowLogMu.Unlock()

	now := time.Now()
	if now.Sub(gonkaSlowLogLastTime) >= time.Second {
		gonkaSlowLogLastTime = now
		gonkaSlowLogCount = 1
		return true
	}

	if gonkaSlowLogCount < limit {
		gonkaSlowLogCount++
		return true
	}
	return false
}

// gonkaMaybeSlowLog emits a structured slow-query log line when enabled and
// the configured threshold is exceeded.
func gonkaMaybeSlowLog(logger log.Logger, r gonkaQueryRecord) {
	cfg := gonkaSlowQueryConfig()
	if !cfg.Enabled {
		return
	}
	if r.totalDuration.Milliseconds() < cfg.ThresholdMS {
		return
	}
	gonkaQuerySlow.WithLabelValues(r.method, r.transport).Inc()
	if !gonkaAllowSlowLog(cfg.RateLimit) {
		return
	}

	requestSummary := gonkaSlowQueryRequestSummary(cfg, r)

	logger.Warn("slow query",
		"method", r.method,
		"transport", r.transport,
		"status", r.status,
		"duration_ms", r.totalDuration.Milliseconds(),
		"setup_ms", r.setupDuration.Milliseconds(),
		"handler_ms", r.handlerDuration.Milliseconds(),
		"gas_used", r.gasConsumed,
		"response_bytes", r.respBytes,
		"requested_height", r.requestedHeight,
		"current_height", r.currentHeight,
		"peer", r.peer,
		"request", requestSummary,
	)
}

func gonkaSlowQueryRequestSummary(cfg telemetry.SlowQueryConfig, r gonkaQueryRecord) string {
	if r.requestSummary == nil {
		return ""
	}

	includeContent := cfg.RequestContent
	requestSummary := r.requestSummary(includeContent)
	requestSummary = gonkaTruncate(requestSummary, cfg.RequestMaxBytes)
	return strings.TrimSpace(requestSummary)
}

// gonkaSlowQueryConfig preserves the original environment variable as an
// override while allowing app.toml to configure the same behavior.
func gonkaSlowQueryConfig() telemetry.SlowQueryConfig {
	cfg := telemetry.GetSlowQueryConfig()

	enabledOverride, enabledOverrideSet := gonkaBoolEnv(gonkaSlowQueryEnabledEnv)

	if v, ok := gonkaEnvValue(gonkaSlowQueryThresholdMSEnv); ok {
		threshold, err := strconv.ParseInt(v, 10, 64)
		switch {
		case err != nil || threshold < 0:
			cfg.ThresholdMS = telemetry.DefaultSlowQueryThresholdMS
			if !enabledOverrideSet {
				cfg.Enabled = true
			}
		case threshold == 0:
			if !enabledOverrideSet {
				cfg.Enabled = false
			}
		default:
			cfg.ThresholdMS = threshold
			if !enabledOverrideSet {
				cfg.Enabled = true
			}
		}
	}

	if enabledOverrideSet {
		cfg.Enabled = enabledOverride
	}
	if rateLimit, ok := gonkaNonNegativeIntEnv(gonkaSlowQueryRateLimitEnv); ok {
		cfg.RateLimit = rateLimit
	}
	if requestContent, ok := gonkaBoolEnv(gonkaSlowQueryRequestContentEnv); ok {
		cfg.RequestContent = requestContent
	}
	if requestMaxBytes, ok := gonkaNonNegativeIntEnv(gonkaSlowQueryRequestMaxBytesEnv); ok {
		cfg.RequestMaxBytes = requestMaxBytes
	}
	return cfg
}

func gonkaEnvValue(name string) (string, bool) {
	v, ok := os.LookupEnv(name)
	return v, ok && v != ""
}

func gonkaBoolEnv(name string) (bool, bool) {
	v, ok := gonkaEnvValue(name)
	if !ok {
		return false, false
	}
	parsed, err := strconv.ParseBool(v)
	return parsed, err == nil
}

func gonkaNonNegativeIntEnv(name string) (int64, bool) {
	v, ok := gonkaEnvValue(name)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseInt(v, 10, 64)
	return parsed, err == nil && parsed >= 0
}

// gonkaSanitizeQueryPath returns a sanitized, low-cardinality path string for metric labels.
func gonkaSanitizeQueryPath(app *BaseApp, requestPath string) string {
	if app.grpcQueryRouter.Route(requestPath) != nil {
		return requestPath
	}

	path := SplitABCIQueryPath(requestPath)
	if len(path) == 0 {
		return "unrecognized"
	}

	switch path[0] {
	case QueryPathApp:
		if len(path) >= 2 && (path[1] == "simulate" || path[1] == "version") {
			return "/app/" + path[1]
		}
		return "unrecognized"

	case QueryPathStore:
		if len(path) >= 2 {
			storeName := path[1]
			var storeExists bool
			if lister, ok := app.cms.(interface {
				GetStoreByName(name string) storetypes.Store
			}); ok {
				storeExists = lister.GetStoreByName(storeName) != nil
			}
			if storeExists {
				return "/store/" + storeName
			}
		}
		return "unrecognized"

	case QueryPathP2P:
		if len(path) >= 4 && path[1] == "filter" && (path[2] == "addr" || path[2] == "id") {
			return "/p2p/filter/" + path[2]
		}
		return "unrecognized"
	}

	return "unrecognized"
}
