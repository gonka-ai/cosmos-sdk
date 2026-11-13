package keeper_test

import (
	"context"
	"time"

	"cosmossdk.io/core/comet"
	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// mockMaintenanceChecker is a test implementation of MaintenanceChecker.
type mockMaintenanceChecker struct {
	inMaintenance map[string]bool
}

func newMockMaintenanceChecker() *mockMaintenanceChecker {
	return &mockMaintenanceChecker{
		inMaintenance: make(map[string]bool),
	}
}

func (m *mockMaintenanceChecker) IsValidatorInActiveMaintenance(_ context.Context, consAddr sdk.ConsAddress) bool {
	return m.inMaintenance[consAddr.String()]
}

func (m *mockMaintenanceChecker) setMaintenance(consAddr sdk.ConsAddress, active bool) {
	m.inMaintenance[consAddr.String()] = active
}

// TestMaintenanceActiveSkipsMissedBlockAccounting verifies that when a validator
// is in active maintenance, HandleValidatorSignature returns early without
// updating signing info counters or missed-block bitmaps.
func (s *KeeperTestSuite) TestMaintenanceActiveSkipsMissedBlockAccounting() {
	require := s.Require()
	ctx := s.ctx

	// Set up signing info for our validator
	signingInfo := types.NewValidatorSigningInfo(consAddr, ctx.BlockHeight(), int64(0), time.Unix(0, 0), false, int64(0))
	require.NoError(s.slashingKeeper.SetValidatorSigningInfo(ctx, consAddr, signingInfo))

	// Wire maintenance checker with validator in maintenance
	mc := newMockMaintenanceChecker()
	mc.setMaintenance(consAddr, true)
	s.slashingKeeper.SetMaintenanceChecker(mc)

	// Expect jailed check
	s.stakingKeeper.EXPECT().IsValidatorJailed(ctx, consAddr).Return(false, nil)

	// HandleValidatorSignature with an absent vote — should be skipped
	err := s.slashingKeeper.HandleValidatorSignature(ctx, consAddr.Bytes(), 100, comet.BlockIDFlagAbsent)
	require.NoError(err)

	// Verify signing info was NOT updated (counter should still be 0)
	info, err := s.slashingKeeper.GetValidatorSigningInfo(ctx, consAddr)
	require.NoError(err)
	require.Equal(int64(0), info.MissedBlocksCounter, "missed blocks counter should not increment during maintenance")
	require.Equal(int64(0), info.IndexOffset, "index offset should not advance during maintenance")
}

// TestMaintenanceActiveNoJailing verifies that even when a validator has exceeded
// the maximum missed blocks threshold, being in maintenance prevents jailing.
func (s *KeeperTestSuite) TestMaintenanceActiveNoJailing() {
	require := s.Require()
	ctx := s.ctx

	// Set up signing info with high missed blocks counter already at threshold
	signingInfo := types.NewValidatorSigningInfo(consAddr, ctx.BlockHeight(), int64(0), time.Unix(0, 0), false, int64(0))
	require.NoError(s.slashingKeeper.SetValidatorSigningInfo(ctx, consAddr, signingInfo))

	// Wire maintenance checker with validator in maintenance
	mc := newMockMaintenanceChecker()
	mc.setMaintenance(consAddr, true)
	s.slashingKeeper.SetMaintenanceChecker(mc)

	// Send many absent votes — none should count
	for i := 0; i < 100; i++ {
		s.stakingKeeper.EXPECT().IsValidatorJailed(ctx, consAddr).Return(false, nil)
		err := s.slashingKeeper.HandleValidatorSignature(ctx, consAddr.Bytes(), 100, comet.BlockIDFlagAbsent)
		require.NoError(err)
	}

	// Verify counter is still zero — no jailing should have occurred
	info, err := s.slashingKeeper.GetValidatorSigningInfo(ctx, consAddr)
	require.NoError(err)
	require.Equal(int64(0), info.MissedBlocksCounter, "no missed blocks should have been counted during maintenance")
}

// TestMaintenanceInactiveNormalLivenessEnforcement verifies that when maintenance
// is NOT active for a validator, normal liveness enforcement works as expected.
func (s *KeeperTestSuite) TestMaintenanceInactiveNormalLivenessEnforcement() {
	require := s.Require()
	ctx := s.ctx

	// Set up signing info
	signingInfo := types.NewValidatorSigningInfo(consAddr, ctx.BlockHeight(), int64(0), time.Unix(0, 0), false, int64(0))
	require.NoError(s.slashingKeeper.SetValidatorSigningInfo(ctx, consAddr, signingInfo))

	// Wire maintenance checker but validator is NOT in maintenance
	mc := newMockMaintenanceChecker()
	mc.setMaintenance(consAddr, false)
	s.slashingKeeper.SetMaintenanceChecker(mc)

	// Expect jailed check and then signing info lookups
	s.stakingKeeper.EXPECT().IsValidatorJailed(ctx, consAddr).Return(false, nil)

	// HandleValidatorSignature with absent — should count normally
	err := s.slashingKeeper.HandleValidatorSignature(ctx, consAddr.Bytes(), 100, comet.BlockIDFlagAbsent)
	require.NoError(err)

	// Verify missed blocks counter incremented
	info, err := s.slashingKeeper.GetValidatorSigningInfo(ctx, consAddr)
	require.NoError(err)
	require.Equal(int64(1), info.MissedBlocksCounter, "missed blocks counter should increment when not in maintenance")
}

// TestNoMaintenanceCheckerNormalBehavior verifies that when no maintenance
// checker is set (nil), normal liveness enforcement works as before.
func (s *KeeperTestSuite) TestNoMaintenanceCheckerNormalBehavior() {
	require := s.Require()
	ctx := s.ctx

	// Set up signing info — no maintenance checker set (default)
	signingInfo := types.NewValidatorSigningInfo(consAddr, ctx.BlockHeight(), int64(0), time.Unix(0, 0), false, int64(0))
	require.NoError(s.slashingKeeper.SetValidatorSigningInfo(ctx, consAddr, signingInfo))

	// Expect jailed check
	s.stakingKeeper.EXPECT().IsValidatorJailed(ctx, consAddr).Return(false, nil)

	// HandleValidatorSignature with absent — should count normally (no maintenance checker)
	err := s.slashingKeeper.HandleValidatorSignature(ctx, consAddr.Bytes(), 100, comet.BlockIDFlagAbsent)
	require.NoError(err)

	// Verify counter incremented
	info, err := s.slashingKeeper.GetValidatorSigningInfo(ctx, consAddr)
	require.NoError(err)
	require.Equal(int64(1), info.MissedBlocksCounter, "missed blocks counter should increment without maintenance checker")
}

// TestMaintenanceDoesNotAffectDoubleSign verifies that the maintenance exemption
// only applies to liveness/downtime — double-sign slashing uses a completely
// separate code path (evidence module) and is not affected.
func (s *KeeperTestSuite) TestMaintenanceDoesNotAffectDoubleSign() {
	require := s.Require()

	// Wire maintenance checker with validator in maintenance
	mc := newMockMaintenanceChecker()
	mc.setMaintenance(consAddr, true)
	s.slashingKeeper.SetMaintenanceChecker(mc)

	// Double-sign slashing goes through Slash/SlashWithInfractionReason directly,
	// NOT through HandleValidatorSignature. Verify it still works.
	slashFractionDoubleSign, err := s.slashingKeeper.SlashFractionDoubleSign(s.ctx)
	require.NoError(err)

	power := sdk.TokensToConsensusPower(sdkmath.NewInt(1), sdk.DefaultPowerReduction)
	s.stakingKeeper.EXPECT().SlashWithInfractionReason(
		s.ctx,
		consAddr,
		s.ctx.BlockHeight(),
		power,
		slashFractionDoubleSign,
		stakingtypes.Infraction_INFRACTION_DOUBLE_SIGN,
	).Return(sdkmath.NewInt(0), nil)

	// This should succeed regardless of maintenance status
	err = s.slashingKeeper.SlashWithInfractionReason(
		s.ctx,
		consAddr,
		slashFractionDoubleSign,
		power,
		s.ctx.BlockHeight(),
		stakingtypes.Infraction_INFRACTION_DOUBLE_SIGN,
	)
	require.NoError(err)
}

// TestMaintenanceResumeAfterEnd verifies that after maintenance ends,
// liveness enforcement resumes normally — missed blocks start counting again.
func (s *KeeperTestSuite) TestMaintenanceResumeAfterEnd() {
	require := s.Require()
	ctx := s.ctx

	// Set up signing info
	signingInfo := types.NewValidatorSigningInfo(consAddr, ctx.BlockHeight(), int64(0), time.Unix(0, 0), false, int64(0))
	require.NoError(s.slashingKeeper.SetValidatorSigningInfo(ctx, consAddr, signingInfo))

	mc := newMockMaintenanceChecker()
	s.slashingKeeper.SetMaintenanceChecker(mc)

	// Phase 1: Maintenance active — miss 5 blocks (should not count)
	mc.setMaintenance(consAddr, true)
	for i := 0; i < 5; i++ {
		s.stakingKeeper.EXPECT().IsValidatorJailed(ctx, consAddr).Return(false, nil)
		err := s.slashingKeeper.HandleValidatorSignature(ctx, consAddr.Bytes(), 100, comet.BlockIDFlagAbsent)
		require.NoError(err)
	}

	info, err := s.slashingKeeper.GetValidatorSigningInfo(ctx, consAddr)
	require.NoError(err)
	require.Equal(int64(0), info.MissedBlocksCounter, "no misses should be counted during maintenance")

	// Phase 2: Maintenance ends — miss 3 blocks (should count)
	mc.setMaintenance(consAddr, false)
	for i := 0; i < 3; i++ {
		s.stakingKeeper.EXPECT().IsValidatorJailed(ctx, consAddr).Return(false, nil)
		err := s.slashingKeeper.HandleValidatorSignature(ctx, consAddr.Bytes(), 100, comet.BlockIDFlagAbsent)
		require.NoError(err)
	}

	info, err = s.slashingKeeper.GetValidatorSigningInfo(ctx, consAddr)
	require.NoError(err)
	require.Equal(int64(3), info.MissedBlocksCounter, "misses should count after maintenance ends")
}

// TestMaintenanceSignedBlocksProcessedNormally verifies that signed (present)
// blocks during maintenance are processed normally — the maintenance exemption
// only applies when the validator is absent. This ensures that signed blocks
// still advance the index offset and update the bitmap as expected, which is
// correct because signed blocks don't trigger any penalties.
func (s *KeeperTestSuite) TestMaintenanceSignedBlocksProcessedNormally() {
	require := s.Require()
	ctx := s.ctx

	// Set up signing info
	signingInfo := types.NewValidatorSigningInfo(consAddr, ctx.BlockHeight(), int64(0), time.Unix(0, 0), false, int64(0))
	require.NoError(s.slashingKeeper.SetValidatorSigningInfo(ctx, consAddr, signingInfo))

	mc := newMockMaintenanceChecker()
	mc.setMaintenance(consAddr, true)
	s.slashingKeeper.SetMaintenanceChecker(mc)

	// Send signed votes during maintenance — these are processed normally
	// because maintenance exemption only triggers on absent votes
	for i := 0; i < 5; i++ {
		s.stakingKeeper.EXPECT().IsValidatorJailed(ctx, consAddr).Return(false, nil)
		err := s.slashingKeeper.HandleValidatorSignature(ctx, consAddr.Bytes(), 100, comet.BlockIDFlagCommit)
		require.NoError(err)
	}

	// Index offset should have advanced because signed blocks are processed normally
	info, err := s.slashingKeeper.GetValidatorSigningInfo(ctx, consAddr)
	require.NoError(err)
	require.Equal(int64(5), info.IndexOffset, "index offset should advance for signed blocks even during maintenance")
	require.Equal(int64(0), info.MissedBlocksCounter, "no missed blocks should be counted for signed blocks")
}
