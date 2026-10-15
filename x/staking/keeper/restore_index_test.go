package keeper_test

import (
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/keeper"
	"github.com/cosmos/cosmos-sdk/x/staking/testutil"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

// Two validators share a consensus key; deleting the one the index points to must
// let the next RestoreValidatorIndex re-point the index to the other.
func (s *KeeperTestSuite) TestRestoreValidatorIndexAfterDeletion() {
	ctx, k := s.ctx.WithBlockHeight(keeper.ValidatorIndexFixHeight), s.stakingKeeper
	require := s.Require()

	a := testutil.NewValidator(s.T(), sdk.ValAddress(PKs[0].Address()), PKs[0])
	b := testutil.NewValidator(s.T(), sdk.ValAddress(PKs[1].Address()), PKs[0])
	a.Status, b.Status = types.Unbonded, types.Unbonded
	a.Tokens, b.Tokens = math.ZeroInt(), math.ZeroInt()
	require.NoError(k.SetValidator(ctx, a))
	require.NoError(k.SetValidator(ctx, b))
	require.NoError(k.SetValidatorByConsAddr(ctx, a))

	consAddr := sdk.ConsAddress(PKs[0].Address())
	k.RestoreValidatorIndex(ctx)
	got, err := k.GetValidatorByConsAddr(ctx, consAddr)
	require.NoError(err)
	require.Equal(a.OperatorAddress, got.OperatorAddress)

	require.NoError(k.RemoveValidator(ctx, sdk.ValAddress(PKs[0].Address())))
	_, err = k.GetValidatorByConsAddr(ctx, consAddr)
	require.ErrorIs(err, types.ErrNoValidatorFound)

	k.RestoreValidatorIndex(ctx)
	got, err = k.GetValidatorByConsAddr(ctx, consAddr)
	require.NoError(err)
	require.Equal(b.OperatorAddress, got.OperatorAddress)
}

// An index broken before the first pass (e.g. state from an older binary) is restored.
func (s *KeeperTestSuite) TestRestoreValidatorIndexFirstPass() {
	ctx, k := s.ctx.WithBlockHeight(keeper.ValidatorIndexFixHeight), s.stakingKeeper
	require := s.Require()

	v := testutil.NewValidator(s.T(), sdk.ValAddress(PKs[2].Address()), PKs[2])
	require.NoError(k.SetValidator(ctx, v))
	k.RestoreValidatorIndex(ctx)
	got, err := k.GetValidatorByConsAddr(ctx, sdk.ConsAddress(PKs[2].Address()))
	require.NoError(err)
	require.Equal(v.OperatorAddress, got.OperatorAddress)
}

// Without a deletion since the last pass, the per-block call reads one key, not every validator.
func (s *KeeperTestSuite) TestRestoreValidatorIndexSkipsWithoutDeletion() {
	ctx, k := s.ctx.WithBlockHeight(keeper.ValidatorIndexFixHeight), s.stakingKeeper
	require := s.Require()

	for i := 3; i < 6; i++ {
		v := testutil.NewValidator(s.T(), sdk.ValAddress(PKs[i].Address()), PKs[i])
		require.NoError(k.SetValidator(ctx, v))
		require.NoError(k.SetValidatorByConsAddr(ctx, v))
	}
	k.RestoreValidatorIndex(ctx)

	ctx = ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	k.RestoreValidatorIndex(ctx)
	require.LessOrEqual(ctx.GasMeter().GasConsumed(), storetypes.KVGasConfig().HasCost)
}
