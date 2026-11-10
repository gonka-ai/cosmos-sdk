package keeper_test

import (
	storetypes "cosmossdk.io/store/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
)

// ApplyComputeValidators writes the same state as SetComputeValidators and only
// skips the final full read of the validator set.
func (s *KeeperTestSuite) TestApplyComputeValidatorsMatchesSetWithoutReread() {
	require := s.Require()
	k := s.stakingKeeper
	ctx := s.ctx.WithBlockHeight(stakingkeeper.ValidatorIndexFixHeight)
	const n = 50

	results := make([]stakingkeeper.ComputeResult, n)
	for i := range results {
		op := sdk.ValAddress(PKs[i].Address())
		results[i] = stakingkeeper.ComputeResult{Power: 10, ValidatorPubKey: PKs[i], OperatorAddress: op.String()}
	}
	_, err := k.SetComputeValidators(ctx, results, false)
	require.NoError(err)
	next := append([]stakingkeeper.ComputeResult{}, results[10:]...)
	next[0].Power = 20

	setCtx, _ := ctx.CacheContext()
	setCtx = setCtx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	want, err := k.SetComputeValidators(setCtx, next, false)
	require.NoError(err)

	applyCtx, _ := ctx.CacheContext()
	applyCtx = applyCtx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	require.NoError(k.ApplyComputeValidators(applyCtx, next, false))
	saved := applyCtx.GasMeter().GasConsumed()

	got, err := k.GetAllValidators(applyCtx)
	require.NoError(err)
	require.Equal(want, got)
	reread := applyCtx.GasMeter().GasConsumed() - saved
	require.Equal(setCtx.GasMeter().GasConsumed(), saved+reread)
	s.T().Logf("set %d gas, apply %d gas", setCtx.GasMeter().GasConsumed(), saved)
}
