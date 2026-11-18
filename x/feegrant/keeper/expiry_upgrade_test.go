package keeper_test

import (
	"encoding/hex"
	"testing"
	"time"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	appmodule "github.com/cosmos/cosmos-sdk/types/module"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/feegrant"
	"github.com/cosmos/cosmos-sdk/x/feegrant/keeper"
	"github.com/cosmos/cosmos-sdk/x/feegrant/module"
	feegranttestutil "github.com/cosmos/cosmos-sdk/x/feegrant/testutil"
)

// This queue key was read from Gonka mainnet at height 7116665. Legacy
// feegrant stored an empty value at this key, before the bool value codec.
const mainnetExpiryQueueKey = "01323032372d30342d33305430353a35343a31332e323639363939373838140005b20e27d86ca8ced73cca05d27be56c6ecc5e143e7343a531796d14b14d99527127a5016b7e5abc"

func TestFeegrantExpiryAfterUpgrade(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		name := "new_keeper_control"
		if legacy {
			name = "legacy_mainnet_queue"
		}
		t.Run(name, func(t *testing.T) {
			key := storetypes.NewKVStoreKey(feegrant.StoreKey)
			testCtx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))
			t.Cleanup(func() { require.NoError(t, testCtx.DB.Close()) })
			expiration, err := time.Parse(time.RFC3339Nano, "2027-04-30T05:54:13.269699788Z")
			require.NoError(t, err)
			ctx := testCtx.Ctx.WithBlockTime(expiration.Add(-time.Hour))
			enc := moduletestutil.MakeTestEncodingConfig(module.AppModuleBasic{})
			rawKey, err := hex.DecodeString(mainnetExpiryQueueKey)
			require.NoError(t, err)
			grantee, granter := sdk.AccAddress(rawKey[31:51]), sdk.AccAddress(rawKey[52:])

			accounts := feegranttestutil.NewMockAccountKeeper(gomock.NewController(t))
			accounts.EXPECT().AddressCodec().Return(address.NewBech32Codec("cosmos")).AnyTimes()
			accounts.EXPECT().GetAccount(gomock.Any(), grantee).Return(authtypes.NewBaseAccountWithAddress(grantee)).AnyTimes()
			k := keeper.NewKeeper(enc.Codec, runtime.NewKVStoreService(key), accounts)
			require.NoError(t, k.GrantAllowance(ctx, granter, grantee, &feegrant.BasicAllowance{Expiration: &expiration}))

			encodedKey, err := collections.EncodeKeyWithPrefix(feegrant.FeeAllowanceQueueKeyPrefix, k.FeeAllowanceQueue.KeyCodec(), collections.Join3(expiration, grantee, granter))
			require.NoError(t, err)
			require.Equal(t, rawKey, encodedKey, "fixture must match the actual mainnet key byte for byte")
			store := ctx.KVStore(key)
			require.Equal(t, []byte{1}, store.Get(rawKey))
			if legacy {
				store.Set(rawKey, []byte{})
				require.True(t, store.Has(rawKey))
				require.Empty(t, store.Get(rawKey))
			}
			grantBefore, err := k.FeeAllowance.Get(ctx, collections.Join(grantee, granter))
			require.NoError(t, err)

			manager := appmodule.NewManager(module.NewAppModule(enc.Codec, accounts, nil, k, enc.InterfaceRegistry))
			msgRouter := baseapp.NewMsgServiceRouter()
			msgRouter.SetInterfaceRegistry(enc.InterfaceRegistry)
			cfg := appmodule.NewConfigurator(enc.Codec, msgRouter, baseapp.NewGRPCQueryRouter())
			require.NoError(t, manager.RegisterServices(cfg))
			versions, err := manager.RunMigrations(ctx, cfg, appmodule.VersionMap{feegrant.ModuleName: 2})
			require.NoError(t, err)
			t.Logf("feegrant version: 2 -> %d; queue value after upgrade: %x", versions[feegrant.ModuleName], store.Get(rawKey))
			grantAfter, err := k.FeeAllowance.Get(ctx, collections.Join(grantee, granter))
			require.NoError(t, err)
			require.Equal(t, grantBefore, grantAfter, "upgrade must preserve the allowance")
			require.True(t, store.Has(rawKey), "upgrade must preserve the expiry queue key")
			require.NoError(t, module.EndBlocker(ctx, k), "an unexpired grant must survive the block")
			require.True(t, store.Has(rawKey))

			err = module.EndBlocker(ctx.WithBlockTime(expiration.Add(time.Nanosecond)), k)
			require.NoError(t, err, "the first block after expiration must succeed")
			require.False(t, store.Has(rawKey), "expired queue entry must be removed")
			hasGrant, err := k.FeeAllowance.Has(ctx, collections.Join(grantee, granter))
			require.NoError(t, err)
			require.False(t, hasGrant, "expired grant must be removed")
		})
	}
}
