package keeper_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtestutil "github.com/cosmos/cosmos-sdk/x/staking/testutil"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// TestSetComputeValidators_SkipsTombstonedValidator checks that, when a tombstone checker is
// wired, the epoch validator-set recompute does not unjail or re-bond a validator the
// slashing module reports as tombstoned. The record is left in place (jailed, unbonded, zero
// power) rather than being routed through updateValidator.
func TestSetComputeValidators_SkipsTombstonedValidator(t *testing.T) {
	key := storetypes.NewKVStoreKey(stakingtypes.StoreKey)
	storeService := runtime.NewKVStoreService(key)
	testCtx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_tombstone"))
	ctx := testCtx.Ctx
	encCfg := moduletestutil.MakeTestEncodingConfig()

	ctrl := gomock.NewController(t)
	ak := stakingtestutil.NewMockAccountKeeper(ctrl)
	ak.EXPECT().GetModuleAddress(stakingtypes.BondedPoolName).Return(bondedAcc.GetAddress()).AnyTimes()
	ak.EXPECT().GetModuleAddress(stakingtypes.NotBondedPoolName).Return(notBondedAcc.GetAddress()).AnyTimes()
	ak.EXPECT().AddressCodec().Return(address.NewBech32Codec("cosmos")).AnyTimes()
	bk := stakingtestutil.NewMockBankKeeper(ctrl)

	k := stakingkeeper.NewKeeper(
		encCfg.Codec, storeService, ak, bk,
		authtypes.NewModuleAddress(govtypes.ModuleName).String(),
		address.NewBech32Codec("cosmosvaloper"),
		address.NewBech32Codec("cosmosvalcons"),
	)
	require.NoError(t, k.SetParams(ctx, stakingtypes.DefaultParams()))
	stakingtypes.RegisterInterfaces(encCfg.InterfaceRegistry)

	consPk := PKs[0]
	valAddr := sdk.ValAddress(PKs[1].Address())
	tombstoned := sdk.ConsAddress(consPk.Address())
	k.SetTombstoneChecker(func(_ context.Context, consAddr sdk.ConsAddress) bool {
		return consAddr.Equals(tombstoned)
	})

	v, err := stakingtypes.NewValidator(valAddr.String(), consPk, stakingtypes.Description{Moniker: "tombstoned"})
	require.NoError(t, err)
	v.Jailed = true
	v.Status = stakingtypes.Unbonded
	v.Tokens = math.ZeroInt()
	v.DelegatorShares = math.LegacyZeroDec()
	require.NoError(t, k.SetValidator(ctx, v))
	require.NoError(t, k.SetValidatorByConsAddr(ctx, v))

	cr := []stakingkeeper.ComputeResult{{OperatorAddress: valAddr.String(), ValidatorPubKey: consPk, Power: 100}}
	_, err = k.SetComputeValidators(ctx, cr, true)
	require.NoError(t, err)

	after, err := k.GetValidator(ctx, valAddr)
	require.NoError(t, err)
	require.True(t, after.Jailed, "tombstoned validator must stay jailed")
	require.False(t, after.IsBonded(), "tombstoned validator must not be re-bonded")
	require.True(t, after.Tokens.IsZero(), "tombstoned validator must not regain power")

	// Halt-safety: evidence/slashing BeginBlock resolves validators by consensus address.
	// If the recompute had physically removed this record, GetValidatorByConsAddr would
	// return ErrNoValidatorFound and FinalizeBlock would halt the chain. The skip is a
	// no-op continue that never deletes, so the cons-addr index still resolves.
	byCons, err := k.GetValidatorByConsAddr(ctx, tombstoned)
	require.NoError(t, err, "tombstoned validator must stay resolvable by cons addr (no evidence/slashing halt)")
	require.Equal(t, valAddr.String(), byCons.OperatorAddress)
}
