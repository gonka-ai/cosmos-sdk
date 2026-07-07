package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-metrics"
	"github.com/stretchr/testify/require"
)

func TestMetrics_Disabled(t *testing.T) {
	m, err := New(Config{Enabled: false})
	require.Nil(t, m)
	require.Nil(t, err)
}

func TestSlowQueryConfig(t *testing.T) {
	t.Cleanup(func() {
		_, _ = New(Config{})
	})

	m, err := New(Config{
		Enabled:                  false,
		SlowQueryEnabled:         true,
		SlowQueryThresholdMS:     750,
		SlowQueryRateLimit:       0,
		SlowQueryRequestContent:  true,
		SlowQueryRequestMaxBytes: 0,
	})
	require.NoError(t, err)
	require.Nil(t, m)
	require.Equal(t, SlowQueryConfig{
		Enabled:         true,
		ThresholdMS:     750,
		RateLimit:       0,
		RequestContent:  true,
		RequestMaxBytes: 0,
	}, GetSlowQueryConfig())
}

func TestSlowQueryConfigDefaultsAndValidation(t *testing.T) {
	cfg, err := normalizeSlowQueryConfig(Config{})
	require.NoError(t, err)
	require.Equal(t, DefaultSlowQueryThresholdMS, cfg.ThresholdMS)
	require.Zero(t, cfg.RateLimit)
	require.Zero(t, cfg.RequestMaxBytes)

	tests := []Config{
		{SlowQueryThresholdMS: -1},
		{SlowQueryRateLimit: -1},
		{SlowQueryRequestMaxBytes: -1},
	}
	for _, tt := range tests {
		_, err := normalizeSlowQueryConfig(tt)
		require.Error(t, err)
	}
}

func TestMetrics_InMem(t *testing.T) {
	m, err := New(Config{
		MetricsSink:    MetricSinkInMem,
		Enabled:        true,
		EnableHostname: false,
		ServiceName:    "test",
	})
	require.NoError(t, err)
	require.NotNil(t, m)

	emitMetrics()

	gr, err := m.Gather(FormatText)
	require.NoError(t, err)
	require.Equal(t, gr.ContentType, "application/json")

	jsonMetrics := make(map[string]any)
	require.NoError(t, json.Unmarshal(gr.Metrics, &jsonMetrics))

	counters := jsonMetrics["Counters"].([]any)
	require.Equal(t, counters[0].(map[string]any)["Count"].(float64), 10.0)
	require.Equal(t, counters[0].(map[string]any)["Name"].(string), "test.dummy_counter")
}

func TestMetrics_Prom(t *testing.T) {
	m, err := New(Config{
		MetricsSink:             MetricSinkInMem,
		Enabled:                 true,
		EnableHostname:          false,
		ServiceName:             "test",
		PrometheusRetentionTime: 60,
		EnableHostnameLabel:     false,
	})
	require.NoError(t, err)
	require.NotNil(t, m)
	require.True(t, m.prometheusEnabled)

	emitMetrics()

	gr, err := m.Gather(FormatPrometheus)
	require.NoError(t, err)
	require.Equal(t, gr.ContentType, string(ContentTypeText))

	require.True(t, strings.Contains(string(gr.Metrics), "test_dummy_counter 30"))
}

func emitMetrics() {
	ticker := time.NewTicker(time.Second)
	timeout := time.After(30 * time.Second)

	for {
		select {
		case <-ticker.C:
			metrics.IncrCounter([]string{"dummy_counter"}, 1.0)
		case <-timeout:
			return
		}
	}
}
