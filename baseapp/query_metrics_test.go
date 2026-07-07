package baseapp

import (
	"fmt"
	"testing"
	"time"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/telemetry"
)

func resetGonkaPeerSet(t *testing.T) {
	t.Helper()

	gonkaPeerMu.Lock()
	defer gonkaPeerMu.Unlock()
	gonkaPeerSet = make(map[string]struct{}, gonkaPeerCardinalityCap)
}

func TestGonkaSanitizePeer(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{
			name: "empty",
			addr: "",
			want: GonkaPeerUnknown,
		},
		{
			name: "invalid host",
			addr: "not-an-ip:1234",
			want: GonkaPeerUnknown,
		},
		{
			name: "ipv4 host port",
			addr: "203.0.113.42:26657",
			want: "203.0.113.0/24",
		},
		{
			name: "ipv4 host only",
			addr: "198.51.100.7",
			want: "198.51.100.0/24",
		},
		{
			name: "bracketed ipv6 host port",
			addr: "[2001:db8:abcd:1234::1]:26657",
			want: "2001:db8:abcd::/48",
		},
		{
			name: "ipv6 host only",
			addr: "2001:db8:ffff:1234::1",
			want: "2001:db8:ffff::/48",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetGonkaPeerSet(t)
			require.Equal(t, tt.want, gonkaSanitizePeer(tt.addr))
		})
	}
}

func TestGonkaSanitizePeerCardinalityCap(t *testing.T) {
	resetGonkaPeerSet(t)

	for i := 0; i < gonkaPeerCardinalityCap; i++ {
		addr := fmt.Sprintf("10.%d.%d.1:26657", i/256, i%256)
		want := fmt.Sprintf("10.%d.%d.0/24", i/256, i%256)
		require.Equal(t, want, gonkaSanitizePeer(addr))
	}

	require.Equal(t, GonkaPeerOverflow, gonkaSanitizePeer("172.16.0.1:26657"))

	// Existing buckets continue to resolve even after the cap has been reached.
	require.Equal(t, "10.0.0.0/24", gonkaSanitizePeer("10.0.0.9:26657"))
}

func TestGonkaSanitizeQueryPath(t *testing.T) {
	name := t.Name()
	db := dbm.NewMemDB()
	logger := log.NewTestLogger(t)
	app := NewBaseApp(name, logger, db, nil)

	capKey := storetypes.NewKVStoreKey("main")
	app.MountStores(capKey)
	require.NoError(t, app.LoadLatestVersion())

	// Test valid static /app/ simulate and version paths
	require.Equal(t, "/app/simulate", gonkaSanitizeQueryPath(app, "/app/simulate"))
	require.Equal(t, "/app/version", gonkaSanitizeQueryPath(app, "/app/version"))
	require.Equal(t, "unrecognized", gonkaSanitizeQueryPath(app, "/app/other"))

	// Test store paths (valid mounted store and invalid ones)
	require.Equal(t, "/store/main", gonkaSanitizeQueryPath(app, "/store/main/key"))
	require.Equal(t, "/store/main", gonkaSanitizeQueryPath(app, "/store/main"))
	require.Equal(t, "unrecognized", gonkaSanitizeQueryPath(app, "/store/other/key"))
	require.Equal(t, "unrecognized", gonkaSanitizeQueryPath(app, "/store/other"))

	// Test p2p filter paths
	require.Equal(t, "/p2p/filter/addr", gonkaSanitizeQueryPath(app, "/p2p/filter/addr/127.0.0.1"))
	require.Equal(t, "/p2p/filter/id", gonkaSanitizeQueryPath(app, "/p2p/filter/id/node_id_val"))
	require.Equal(t, "unrecognized", gonkaSanitizeQueryPath(app, "/p2p/filter/addr")) // lacks argument
	require.Equal(t, "unrecognized", gonkaSanitizeQueryPath(app, "/p2p/filter/id"))   // lacks argument
	require.Equal(t, "unrecognized", gonkaSanitizeQueryPath(app, "/p2p/filter/invalid/val"))
	require.Equal(t, "unrecognized", gonkaSanitizeQueryPath(app, "/p2p/other"))

	// Test completely unrecognized paths
	require.Equal(t, "unrecognized", gonkaSanitizeQueryPath(app, "/foo/bar"))
	require.Equal(t, "unrecognized", gonkaSanitizeQueryPath(app, ""))
	for i := 0; i < 1_000; i++ {
		require.Equal(t, "unrecognized", gonkaSanitizeQueryPath(app, fmt.Sprintf("/attacker/%d", i)))
	}
}

func TestGonkaAllowSlowLog(t *testing.T) {
	// Reset state
	gonkaSlowLogMu.Lock()
	gonkaSlowLogLastTime = time.Time{}
	gonkaSlowLogCount = 0
	gonkaSlowLogMu.Unlock()

	// Initial 5 calls should be allowed
	for i := 0; i < 5; i++ {
		require.True(t, gonkaAllowSlowLog(5))
	}
	// Sixth check in same second window should be rate-limited
	require.False(t, gonkaAllowSlowLog(5))

	// Zero disables the rate limit.
	for i := 0; i < 100; i++ {
		require.True(t, gonkaAllowSlowLog(0))
	}
}

func TestGonkaSlowQueryRequestSummary(t *testing.T) {
	rec := gonkaQueryRecord{
		requestSummary: func(includeContent bool) string {
			if includeContent {
				return "secret"
			}
			return "type=request size=4B"
		},
	}

	require.Equal(t, "type=request size=4B", gonkaSlowQueryRequestSummary(telemetry.SlowQueryConfig{}, rec))
	require.Equal(t, "secret", gonkaSlowQueryRequestSummary(telemetry.SlowQueryConfig{
		RequestContent: true,
	}, rec))
	require.Equal(t, "secr...(truncated)", gonkaSlowQueryRequestSummary(telemetry.SlowQueryConfig{
		RequestContent:  true,
		RequestMaxBytes: 4,
	}, rec))
}

func TestGonkaSlowQueryConfigEnvironmentOverride(t *testing.T) {
	clearGonkaSlowQueryEnv(t)
	t.Cleanup(func() {
		_, _ = telemetry.New(telemetry.Config{})
	})
	_, err := telemetry.New(telemetry.Config{
		SlowQueryEnabled:         true,
		SlowQueryThresholdMS:     600,
		SlowQueryRequestContent:  true,
		SlowQueryRequestMaxBytes: telemetry.DefaultSlowQueryRequestMaxBytes,
	})
	require.NoError(t, err)

	tests := []struct {
		name          string
		env           string
		wantEnabled   bool
		wantThreshold int64
	}{
		{name: "unset", env: "", wantEnabled: true, wantThreshold: 600},
		{name: "zero disables", env: "0", wantEnabled: false, wantThreshold: 600},
		{name: "positive overrides", env: "750", wantEnabled: true, wantThreshold: 750},
		{name: "invalid keeps legacy default", env: "invalid", wantEnabled: true, wantThreshold: telemetry.DefaultSlowQueryThresholdMS},
		{name: "negative keeps legacy default", env: "-1", wantEnabled: true, wantThreshold: telemetry.DefaultSlowQueryThresholdMS},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(gonkaSlowQueryThresholdMSEnv, tt.env)
			cfg := gonkaSlowQueryConfig()
			require.Equal(t, tt.wantEnabled, cfg.Enabled)
			require.Equal(t, tt.wantThreshold, cfg.ThresholdMS)
		})
	}
}

func TestGonkaSlowQueryConfigAllEnvironmentOverrides(t *testing.T) {
	clearGonkaSlowQueryEnv(t)
	t.Cleanup(func() {
		_, _ = telemetry.New(telemetry.Config{})
	})
	_, err := telemetry.New(telemetry.Config{
		SlowQueryEnabled:         true,
		SlowQueryThresholdMS:     600,
		SlowQueryRateLimit:       5,
		SlowQueryRequestContent:  true,
		SlowQueryRequestMaxBytes: telemetry.DefaultSlowQueryRequestMaxBytes,
	})
	require.NoError(t, err)

	t.Setenv(gonkaSlowQueryEnabledEnv, "false")
	t.Setenv(gonkaSlowQueryThresholdMSEnv, "750")
	t.Setenv(gonkaSlowQueryRateLimitEnv, "0")
	t.Setenv(gonkaSlowQueryRequestContentEnv, "false")
	t.Setenv(gonkaSlowQueryRequestMaxBytesEnv, "0")

	cfg := gonkaSlowQueryConfig()
	require.False(t, cfg.Enabled)
	require.Equal(t, int64(750), cfg.ThresholdMS)
	require.Zero(t, cfg.RateLimit)
	require.False(t, cfg.RequestContent)
	require.Zero(t, cfg.RequestMaxBytes)
}

func TestGonkaSlowQueryConfigInvalidNewEnvironmentOverridesUseConfig(t *testing.T) {
	clearGonkaSlowQueryEnv(t)
	t.Cleanup(func() {
		_, _ = telemetry.New(telemetry.Config{})
	})
	_, err := telemetry.New(telemetry.Config{
		SlowQueryEnabled:         true,
		SlowQueryThresholdMS:     600,
		SlowQueryRateLimit:       5,
		SlowQueryRequestContent:  true,
		SlowQueryRequestMaxBytes: telemetry.DefaultSlowQueryRequestMaxBytes,
	})
	require.NoError(t, err)

	t.Setenv(gonkaSlowQueryEnabledEnv, "invalid")
	t.Setenv(gonkaSlowQueryRateLimitEnv, "-1")
	t.Setenv(gonkaSlowQueryRequestContentEnv, "invalid")
	t.Setenv(gonkaSlowQueryRequestMaxBytesEnv, "-1")

	cfg := gonkaSlowQueryConfig()
	require.True(t, cfg.Enabled)
	require.Equal(t, int64(600), cfg.ThresholdMS)
	require.Equal(t, int64(5), cfg.RateLimit)
	require.True(t, cfg.RequestContent)
	require.Equal(t, telemetry.DefaultSlowQueryRequestMaxBytes, cfg.RequestMaxBytes)
}

func clearGonkaSlowQueryEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		gonkaSlowQueryEnabledEnv,
		gonkaSlowQueryThresholdMSEnv,
		gonkaSlowQueryRateLimitEnv,
		gonkaSlowQueryRequestContentEnv,
		gonkaSlowQueryRequestMaxBytesEnv,
	} {
		t.Setenv(name, "")
	}
}

func TestGonkaSlowQueryDisabledDoesNotFormatRequest(t *testing.T) {
	clearGonkaSlowQueryEnv(t)
	t.Cleanup(func() {
		_, _ = telemetry.New(telemetry.Config{})
	})

	_, err := telemetry.New(telemetry.Config{
		SlowQueryEnabled:     false,
		SlowQueryThresholdMS: 1,
	})
	require.NoError(t, err)

	formatted := false
	gonkaMaybeSlowLog(log.NewNopLogger(), gonkaQueryRecord{
		totalDuration: 2 * time.Millisecond,
		requestSummary: func(bool) string {
			formatted = true
			return "request"
		},
	})
	require.False(t, formatted)
}
