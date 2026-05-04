package baseapp

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
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
