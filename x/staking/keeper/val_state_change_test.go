package keeper_test

import (
	"cosmossdk.io/math"

	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// TestDeleteValidatorInternal_PreservesReassignedConsAddrIndex exercises the
// GON-191 defensive check: when a stale (tokens=0) validator's main entry is
// being cleaned up but the cons-addr index has already been reassigned in the
// same block to a new validator (via filterBasedOnExisting's stale-conflict
// resolution path), the cleanup must NOT delete the cons-addr index entry that
// the new validator now owns.
//
// Without the defensive check, the new validator would be orphaned from any
// lookup-by-consensus-address (slashing, evidence handling, jailing), which is
// the root cause of the symptom GON-191 set out to fix.
func (s *KeeperTestSuite) TestDeleteValidatorInternal_PreservesReassignedConsAddrIndex() {
	ctx, keeper := s.ctx, s.stakingKeeper
	require := s.Require()

	pks := simtestutil.CreateTestPubKeys(3)
	sharedConsPK := pks[0]
	opA := sdk.ValAddress(pks[1].Address())
	opB := sdk.ValAddress(pks[2].Address())

	// Stale validator A with the shared consensus key, tokens=0.
	valA, err := stakingtypes.NewValidator(opA.String(), sharedConsPK, stakingtypes.Description{Moniker: "opA"})
	require.NoError(err)
	valA.Status = stakingtypes.Unbonded
	valA.Tokens = math.ZeroInt()
	require.NoError(keeper.SetValidator(ctx, valA))
	require.NoError(keeper.SetValidatorByConsAddr(ctx, valA))

	// New validator B claims the same consensus key. SetValidatorByConsAddr
	// overwrites the cons-addr index to point at B's operator address.
	valB, err := stakingtypes.NewValidator(opB.String(), sharedConsPK, stakingtypes.Description{Moniker: "opB"})
	require.NoError(err)
	valB.Status = stakingtypes.Bonded
	valB.Tokens = math.NewInt(100)
	require.NoError(keeper.SetValidator(ctx, valB))
	require.NoError(keeper.SetValidatorByConsAddr(ctx, valB))

	consAddr, err := valA.GetConsAddr()
	require.NoError(err)
	owner, err := keeper.GetValidatorByConsAddr(ctx, consAddr)
	require.NoError(err)
	require.Equal(opB.String(), owner.OperatorAddress, "precondition: cons-addr index must point to B after overwrite")

	// Delete the stale validator. Without the defensive check this would also
	// erase the cons-addr index that now points at B.
	require.NoError(keeper.DeleteValidatorInternalForTest(ctx, valA, opA))

	ownerAfter, err := keeper.GetValidatorByConsAddr(ctx, consAddr)
	require.NoError(err, "cons-addr index must still resolve after stale cleanup")
	require.Equal(opB.String(), ownerAfter.OperatorAddress, "cons-addr index must still point to B; deleting stale A must not orphan B")

	// Stale A's main store entry is gone.
	_, err = keeper.GetValidator(ctx, opA)
	require.Error(err, "stale validator A's main store entry should be removed")
}

// TestDeleteValidatorInternal_DeletesUnreassignedConsAddrIndex confirms that the
// defensive check does not break the normal cleanup path: when no other
// validator has claimed the cons-addr index, deletion still removes it.
func (s *KeeperTestSuite) TestDeleteValidatorInternal_DeletesUnreassignedConsAddrIndex() {
	ctx, keeper := s.ctx, s.stakingKeeper
	require := s.Require()

	pks := simtestutil.CreateTestPubKeys(2)
	consPK := pks[0]
	op := sdk.ValAddress(pks[1].Address())

	val, err := stakingtypes.NewValidator(op.String(), consPK, stakingtypes.Description{Moniker: "op"})
	require.NoError(err)
	val.Status = stakingtypes.Unbonded
	val.Tokens = math.ZeroInt()
	require.NoError(keeper.SetValidator(ctx, val))
	require.NoError(keeper.SetValidatorByConsAddr(ctx, val))

	consAddr, err := val.GetConsAddr()
	require.NoError(err)

	owner, err := keeper.GetValidatorByConsAddr(ctx, consAddr)
	require.NoError(err)
	require.Equal(op.String(), owner.OperatorAddress)

	require.NoError(keeper.DeleteValidatorInternalForTest(ctx, val, op))

	_, err = keeper.GetValidatorByConsAddr(ctx, consAddr)
	require.Error(err, "cons-addr index should be deleted when this validator still owned it")
}
