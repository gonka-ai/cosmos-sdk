package keeper_test

import (
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

// An epoch that leaves already-zeroed validators out of the compute results again
// does not rewrite them, and does not reopen the zero-power sweep.
func (s *KeeperTestSuite) TestSetComputeValidatorsSkipsAlreadyZeroed() {
	require := s.Require()
	k := s.stakingKeeper
	ctx := s.ctx.WithBlockHeight(stakingkeeper.ValidatorIndexFixHeight)
	const n = 50

	results := make([]stakingkeeper.ComputeResult, n+1)
	ops := make([]sdk.ValAddress, n+1)
	for i := range results {
		ops[i] = sdk.ValAddress(PKs[i].Address())
		results[i] = stakingkeeper.ComputeResult{Power: 10, ValidatorPubKey: PKs[i], OperatorAddress: ops[i].String()}
	}
	live := results[n:]

	_, err := k.SetComputeValidators(ctx, results, false)
	require.NoError(err)
	_, err = k.SetComputeValidators(ctx, live, false) // zeroes the other n
	require.NoError(err)
	for _, op := range ops[:n] { // kept as on mainnet (cosmos-sdk#14)
		require.NoError(k.SetLastValidatorPower(ctx, op, 0))
	}
	require.NoError(k.DeleteZeroPowerValidators(ctx))

	// one zombie still holds self-delegation shares and must be rewritten
	del, err := k.GetDelegation(ctx, sdk.AccAddress(ops[0]), ops[0])
	require.NoError(err)
	del.Shares = math.LegacyNewDec(5)
	require.NoError(k.SetDelegation(ctx, del))

	before := make([]types.Validator, n)
	for i, op := range ops[:n] {
		before[i], err = k.GetValidator(ctx, op)
		require.NoError(err)
		require.True(before[i].Tokens.IsZero())
	}

	epochCtx := ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	_, err = k.SetComputeValidators(epochCtx, live, false)
	require.NoError(err)
	s.T().Logf("%d zeroed validators: repeat epoch %d gas", n, epochCtx.GasMeter().GasConsumed())

	for i, op := range ops[:n] {
		after, err := k.GetValidator(ctx, op)
		require.NoError(err)
		require.Equal(before[i], after)
	}
	del, err = k.GetDelegation(ctx, sdk.AccAddress(ops[0]), ops[0])
	require.NoError(err)
	require.True(del.Shares.IsZero())

	// ops[0] was rewritten with zero tokens, so the sweep reruns once and then goes quiet
	require.NoError(k.DeleteZeroPowerValidators(ctx))
	quiet := ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	require.NoError(k.DeleteZeroPowerValidators(quiet))
	require.LessOrEqual(quiet.GasMeter().GasConsumed(), storetypes.KVGasConfig().HasCost)
}

// Without any zombie needing a write the repeat epoch leaves the sweep marker set.
func (s *KeeperTestSuite) TestSetComputeValidatorsRepeatEpochKeepsSweepQuiet() {
	require := s.Require()
	k := s.stakingKeeper
	ctx := s.ctx.WithBlockHeight(stakingkeeper.ValidatorIndexFixHeight)

	results := make([]stakingkeeper.ComputeResult, 4)
	for i := range results {
		op := sdk.ValAddress(PKs[i].Address())
		results[i] = stakingkeeper.ComputeResult{Power: 10, ValidatorPubKey: PKs[i], OperatorAddress: op.String()}
	}
	_, err := k.SetComputeValidators(ctx, results, false)
	require.NoError(err)
	_, err = k.SetComputeValidators(ctx, results[3:], false)
	require.NoError(err)
	for _, r := range results[:3] {
		op, err := k.ValidatorAddressCodec().StringToBytes(r.OperatorAddress)
		require.NoError(err)
		require.NoError(k.SetLastValidatorPower(ctx, op, 0))
	}
	require.NoError(k.DeleteZeroPowerValidators(ctx))

	_, err = k.SetComputeValidators(ctx, results[3:], false)
	require.NoError(err)
	quiet := ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	require.NoError(k.DeleteZeroPowerValidators(quiet))
	require.LessOrEqual(quiet.GasMeter().GasConsumed(), storetypes.KVGasConfig().HasCost)
}
