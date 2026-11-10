package keeper_test

import (
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/testutil"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

// zeroPowerValidators stores n validators with zero tokens; the first inLast of them
// keep a LastValidatorPower entry, as on mainnet where ~2,170 such records accumulate.
func (s *KeeperTestSuite) zeroPowerValidators(n, inLast int) []types.Validator {
	ctx, k := s.ctx, s.stakingKeeper
	vals := make([]types.Validator, n)
	for i := range vals {
		pk := ed25519.GenPrivKey().PubKey()
		v := testutil.NewValidator(s.T(), sdk.ValAddress(pk.Address()), pk)
		v.Status, v.Tokens, v.DelegatorShares = types.Bonded, math.ZeroInt(), math.LegacyZeroDec()
		s.Require().NoError(k.SetValidator(ctx, v))
		if i < inLast {
			s.Require().NoError(k.SetLastValidatorPower(ctx, sdk.ValAddress(pk.Address()), 0))
		}
		vals[i] = v
	}
	return vals
}

func (s *KeeperTestSuite) sweepGas() uint64 {
	ctx := s.ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	s.Require().NoError(s.stakingKeeper.DeleteZeroPowerValidators(ctx))
	return ctx.GasMeter().GasConsumed()
}

func (s *KeeperTestSuite) countValidators() int {
	all, err := s.stakingKeeper.GetAllValidators(s.ctx)
	s.Require().NoError(err)
	return len(all)
}

// A block with no validator zeroed and none leaving LastValidatorPower reads one key.
func (s *KeeperTestSuite) TestDeleteZeroPowerValidatorsSkipsQuietBlock() {
	s.zeroPowerValidators(50, 10)

	first := s.sweepGas()
	quiet := s.sweepGas()
	s.T().Logf("50 validators: full pass %d gas, quiet block %d gas", first, quiet)
	s.Require().Greater(first, 50*storetypes.KVGasConfig().IterNextCostFlat)
	s.Require().LessOrEqual(quiet, storetypes.KVGasConfig().HasCost)
}

// Zeroing a validator or dropping one from LastValidatorPower reruns the full pass,
// and a pass forced by clearing the marker leaves the same validators.
func (s *KeeperTestSuite) TestDeleteZeroPowerValidatorsRescansAfterChange() {
	require := s.Require()
	ctx, k := s.ctx, s.stakingKeeper
	has := storetypes.KVGasConfig().HasCost
	vals := s.zeroPowerValidators(20, 5)
	pk := ed25519.GenPrivKey().PubKey()
	live := testutil.NewValidator(s.T(), sdk.ValAddress(pk.Address()), pk)
	live.Status, live.Tokens = types.Bonded, math.NewInt(10)
	require.NoError(k.SetValidator(ctx, live))
	s.sweepGas()
	require.LessOrEqual(s.sweepGas(), has)

	live.Tokens = math.ZeroInt()
	require.NoError(k.SetValidator(ctx, live))
	require.Greater(s.sweepGas(), has)
	require.LessOrEqual(s.sweepGas(), has)

	valAddr, err := k.ValidatorAddressCodec().StringToBytes(vals[0].GetOperator())
	require.NoError(err)
	require.NoError(k.DeleteLastValidatorPower(ctx, valAddr))
	require.Greater(s.sweepGas(), has)
	require.LessOrEqual(s.sweepGas(), has)

	after := s.countValidators()
	require.NoError(k.DeleteLastValidatorPower(ctx, sdk.ValAddress("absent-operator-addr")))
	require.Greater(s.sweepGas(), has)
	require.Equal(after, s.countValidators())
}
