package keeper_test

import (
	"bytes"

	"cosmossdk.io/log"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/testutil"
)

// An EndBlock with no validator set change must not log at Info: the loop runs every block.
func (s *KeeperTestSuite) TestApplyAndReturnValidatorSetUpdatesQuietWithoutChanges() {
	ctx, keeper := s.ctx, s.stakingKeeper
	require := s.Require()

	for i, power := range []int64{10, 5, 0} {
		valAddr := sdk.ValAddress(PKs[i].Address().Bytes())
		validator := testutil.NewValidator(s.T(), valAddr, PKs[i])
		validator, _ = validator.AddTokensFromDel(keeper.TokensFromConsensusPower(ctx, power))
		require.NoError(keeper.SetValidator(ctx, validator))
		require.NoError(keeper.SetValidatorByPowerIndex(ctx, validator))
	}
	s.applyValidatorSetUpdates(ctx, keeper, 2)

	var buf bytes.Buffer
	level, err := log.ParseLogLevel("info")
	require.NoError(err)
	ctx = ctx.WithLogger(log.NewLogger(&buf, log.FilterOption(level)))
	s.applyValidatorSetUpdates(ctx, keeper, 0)
	require.Empty(buf.String())

	// zero-power validator still stops the loop
	vals, err := keeper.GetLastValidators(ctx)
	require.NoError(err)
	require.Len(vals, 2)
	require.True(vals[0].Tokens.GT(math.ZeroInt()))
}
