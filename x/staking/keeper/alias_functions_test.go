package keeper_test

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/testutil"
)

// TestValidatorByConsAddr_MissingValidatorIsNotAnError asserts that a lookup for
// a consensus address with no validator behind it reports (nil, nil) instead of
// ErrNoValidatorFound.
//
// The two non-test callers of ValidatorByConsAddr both run inside BeginBlock and
// both already handle a nil validator gracefully -- x/evidence
// handleEquivocationEvidence ("Defensive: Simulation doesn't take unbonding
// periods into account, and CometBFT might break this assumption at some point")
// and x/slashing HandleValidatorSignature ("if validator != nil"). Neither guard
// is reachable while the error is propagated: the `if err != nil { return err }`
// check comes first, the error travels out of BeginBlocker into FinalizeBlock and
// the chain halts with "validator does not exist".
//
// Upstream cosmos never reaches that state, because a validator is removed only
// after the full unbonding period, long after it has left every commit. This
// fork's compute-validator model deletes validators with no unbonding period, so
// a consensus address that was valid one block ago can be gone in the next one.
func (s *KeeperTestSuite) TestValidatorByConsAddr_MissingValidatorIsNotAnError() {
	ctx, keeper := s.ctx, s.stakingKeeper
	require := s.Require()

	// A consensus address that was never registered.
	unknownConsAddr := sdk.ConsAddress(PKs[0].Address())

	validator, err := keeper.ValidatorByConsAddr(ctx, unknownConsAddr)
	require.NoError(err, "a missing validator must not surface as an error: it halts BeginBlocker")
	require.Nil(validator, "a missing validator must be reported as a nil ValidatorI")
}

// TestValidatorByConsAddr_ExistingValidatorIsReturned is the companion check: the
// nil-on-missing contract must not swallow a validator that is actually there.
func (s *KeeperTestSuite) TestValidatorByConsAddr_ExistingValidatorIsReturned() {
	ctx, keeper := s.ctx, s.stakingKeeper
	require := s.Require()

	valPubKey := PKs[1]
	valAddr := sdk.ValAddress(valPubKey.Address().Bytes())
	consAddr := sdk.ConsAddress(valPubKey.Address())

	validator := testutil.NewValidator(s.T(), valAddr, valPubKey)
	require.NoError(keeper.SetValidator(ctx, validator))
	require.NoError(keeper.SetValidatorByConsAddr(ctx, validator))

	got, err := keeper.ValidatorByConsAddr(ctx, consAddr)
	require.NoError(err)
	require.NotNil(got)
	require.Equal(validator.GetOperator(), got.GetOperator())
}
