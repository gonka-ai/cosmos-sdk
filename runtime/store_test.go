package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
)

// A transient store opened through the service is priced by TransientGasConfig,
// as ctx.TransientStore is, not by the persistent KVGasConfig.
func TestTransientStoreServiceGas(t *testing.T) {
	key := storetypes.NewKVStoreKey("kv")
	tkey := storetypes.NewTransientStoreKey("transient:test")
	ctx := testutil.DefaultContext(key, tkey)
	svc := runtime.NewTransientStoreService(tkey)

	k, v := []byte{0x03}, make([]byte, 88)
	cfg := storetypes.TransientGasConfig()

	ctx = ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	require.NoError(t, svc.OpenTransientStore(ctx).Set(k, v))
	require.Equal(t, cfg.WriteCostFlat+cfg.WriteCostPerByte*uint64(len(k)+len(v)), ctx.GasMeter().GasConsumed())

	ctx = ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	got, err := svc.OpenTransientStore(ctx).Get(k)
	require.NoError(t, err)
	require.Equal(t, v, got)
	require.Equal(t, cfg.ReadCostFlat+cfg.ReadCostPerByte*uint64(len(k)+len(v)), ctx.GasMeter().GasConsumed())
}
