package keeper_test

import (
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"go.uber.org/mock/gomock"

	"cosmossdk.io/core/comet"
	storetypes "cosmossdk.io/store/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/slashing"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
)

// TestBeginBlockerReadsParamsOnce verifies that BeginBlocker reads the module
// params once per block, not once or more per vote.
func (s *KeeperTestSuite) TestBeginBlockerReadsParamsOnce() {
	require := s.Require()

	addrs := make([]sdk.ConsAddress, 4)
	for i := range addrs {
		addrs[i] = sdk.ConsAddress([]byte{byte('a' + i), '_', '_', '_', '_', '_', '_', '_', '_', '_', '_', '_', '_', '_', '_', '_', '_', '_', '_', '_'})
		info := slashingtypes.NewValidatorSigningInfo(addrs[i], 0, 0, time.Unix(0, 0), false, 0)
		require.NoError(s.slashingKeeper.SetValidatorSigningInfo(s.ctx, addrs[i], info))
	}
	s.stakingKeeper.EXPECT().IsValidatorJailed(gomock.Any(), gomock.Any()).Return(false, nil).AnyTimes()
	s.stakingKeeper.EXPECT().RestoreValidatorIndex(gomock.Any()).AnyTimes()

	gasOf := func(f func(ctx sdk.Context)) storetypes.Gas {
		ctx := s.ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
		f(ctx)
		return ctx.GasMeter().GasConsumed()
	}

	params := gasOf(func(ctx sdk.Context) {
		_, err := s.slashingKeeper.GetParams(ctx)
		require.NoError(err)
	})
	one := gasOf(func(ctx sdk.Context) {
		require.NoError(s.slashingKeeper.HandleValidatorSignature(ctx, addrs[0].Bytes(), 10, comet.BlockIDFlagCommit))
	})
	votes := make([]abci.VoteInfo, 0, 3)
	for _, a := range addrs[1:] {
		votes = append(votes, abci.VoteInfo{Validator: abci.Validator{Address: a, Power: 10}, BlockIdFlag: 2})
	}
	block := gasOf(func(ctx sdk.Context) {
		require.NoError(slashing.BeginBlocker(ctx.WithVoteInfos(votes), s.slashingKeeper))
	})

	require.NotZero(params)
	require.Equal(params+3*(one-params), block)
}
