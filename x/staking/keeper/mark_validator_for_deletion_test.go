package keeper_test

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	"github.com/cosmos/cosmos-sdk/x/staking/testutil"
)

// TestMarkValidatorForDeletion_JailedRemoval_KeepsValidatorReachable asserts
// that when SetComputeValidators removes a jailed validator, the validator's
// consensus-address index remains reachable. CometBFT continues to include
// the validator in LastCommit for ValidatorUpdateDelay blocks after the
// staking module decides to remove it (= header.Height + 1 + 1 per cometbft
// state/execution.go, matching the NextValidators double-buffer), and during
// that window slashing.BeginBlocker → IsValidatorJailed →
// GetValidatorByConsAddr must succeed — otherwise the chain halts with
// ErrNoValidatorFound on the very next block.
//
// On current code this test FAILS: the jailed branch of
// markValidatorForDeletion (compute.go:559-563) routes through
// deleteValidatorInternal, which wipes the staking record AND the
// ValidatorByConsAddr index immediately, while CometBFT is still sending
// votes for the validator. The non-jailed branch (compute.go:565-598) keeps
// the record at zero power, so it does not have this problem.
func (s *KeeperTestSuite) TestMarkValidatorForDeletion_JailedRemoval_KeepsValidatorReachable() {
	ctx, keeper := s.ctx, s.stakingKeeper
	require := s.Require()

	// Position past ValidatorIndexFixHeight so we exercise the post-fix path.
	ctx = ctx.WithBlockHeight(stakingkeeper.ValidatorIndexFixHeight + 1)

	valPubKey := PKs[0]
	valAddr := sdk.ValAddress(valPubKey.Address().Bytes())
	consAddr := sdk.ConsAddress(valPubKey.Address())
	valTokens := keeper.TokensFromConsensusPower(ctx, 10)

	validator := testutil.NewValidator(s.T(), valAddr, valPubKey)
	validator, _ = validator.AddTokensFromDel(valTokens)
	require.NoError(keeper.SetValidator(ctx, validator))
	require.NoError(keeper.SetValidatorByPowerIndex(ctx, validator))
	require.NoError(keeper.SetValidatorByConsAddr(ctx, validator))

	// Unbonded -> Bonded. gonka's NoOpBankKeeper (keeper.go:113) swallows
	// pool transfers, so no bank EXPECT is needed.
	_, err := keeper.ApplyAndReturnValidatorSetUpdates(ctx)
	require.NoError(err)

	// Pre-condition: the validator is reachable via consensus address.
	_, err = keeper.GetValidatorByConsAddr(ctx, consAddr)
	require.NoError(err)

	// Jail the validator. jailValidator also calls DeleteValidatorByPowerIndex,
	// so afterwards it is in the store with Jailed=true but not in the power
	// index. Crucially the record AND the ValidatorByConsAddr index still
	// exist — matching CometBFT's view that this validator is still in the
	// active set.
	require.NoError(keeper.Jail(ctx, consAddr))
	_, err = keeper.GetValidatorByConsAddr(ctx, consAddr)
	require.NoError(err, "Jail itself must not wipe the index")

	// Drop the jailed validator from compute results. This is the operation
	// under test — markValidatorForDeletion takes the jailed branch.
	_, err = keeper.SetComputeValidators(ctx, []stakingkeeper.ComputeResult{}, true)
	require.NoError(err)

	// === The assertion under test (FAILS on current code) =================
	// At this point CometBFT still has the validator in its active set and
	// will continue to include it in LastCommit for ValidatorUpdateDelay
	// blocks. slashing.BeginBlocker walks each vote and calls
	// GetValidatorByConsAddr — that lookup MUST succeed; otherwise the chain
	// halts. Therefore the index must NOT be wiped immediately when the
	// staking module decides to remove the validator.
	_, err = keeper.GetValidatorByConsAddr(ctx, consAddr)
	require.NoError(err,
		"jailed-validator removal must not wipe the ValidatorByConsAddr "+
			"index immediately: CometBFT continues to include the validator "+
			"in LastCommit for ValidatorUpdateDelay blocks, and slashing's "+
			"GetValidatorByConsAddr lookups must succeed during that "+
			"window — otherwise BeginBlocker halts the chain with "+
			"ErrNoValidatorFound.")
}
