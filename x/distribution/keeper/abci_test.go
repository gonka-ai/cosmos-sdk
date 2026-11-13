package keeper_test

import (
	"fmt"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/distribution"
	"github.com/cosmos/cosmos-sdk/x/distribution/keeper"
	distrtestutil "github.com/cosmos/cosmos-sdk/x/distribution/testutil"
	types "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

var testProposerAddress = sdk.ConsAddress("test")

type testSetup struct {
	testCtx       testutil.TestContext
	bankKeeper    *distrtestutil.MockBankKeeper
	stakingKeeper *distrtestutil.MockStakingKeeper
	accountKeeper *distrtestutil.MockAccountKeeper
	distrKeeper   keeper.Keeper
}

func setupTest(t *testing.T) testSetup {
	t.Helper()

	ctrl := gomock.NewController(t)
	key := storetypes.NewKVStoreKey(types.StoreKey)
	storeService := runtime.NewKVStoreService(key)
	testCtx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))
	encCfg := moduletestutil.MakeTestEncodingConfig(distribution.AppModuleBasic{})

	bankKeeper := distrtestutil.NewMockBankKeeper(ctrl)
	stakingKeeper := distrtestutil.NewMockStakingKeeper(ctrl)
	accountKeeper := distrtestutil.NewMockAccountKeeper(ctrl)

	accountKeeper.EXPECT().GetModuleAccount(gomock.Any(), "fee_collector").Return(feeCollectorAcc).AnyTimes()
	stakingKeeper.EXPECT().ValidatorAddressCodec().Return(address.NewBech32Codec("cosmosvaloper")).AnyTimes()
	accountKeeper.EXPECT().GetModuleAddress(types.ModuleName).Return(distrAcc.GetAddress()).AnyTimes()

	distrKeeper := keeper.NewKeeper(
		encCfg.Codec,
		storeService,
		accountKeeper,
		bankKeeper,
		stakingKeeper,
		"fee_collector",
		authtypes.NewModuleAddress("gov").String(),
	)

	// empty initialize
	err := distrKeeper.FeePool.Set(testCtx.Ctx, types.FeePool{
		CommunityPool: sdk.NewDecCoins(),
	})
	require.NoError(t, err)

	return testSetup{
		testCtx:       testCtx,
		bankKeeper:    bankKeeper,
		distrKeeper:   distrKeeper,
		stakingKeeper: stakingKeeper,
		accountKeeper: accountKeeper,
	}
}

// Scenario:
// check no distribution occurs after begin block with no extra validator state
func TestBeginBlockNoOp(t *testing.T) {
	ts := setupTest(t)
	ctx := ts.testCtx.Ctx.
		WithBlockHeader(cmtproto.Header{
			ProposerAddress: testProposerAddress,
			Time:            time.Now(),
		}).
		WithBlockHeight(0)

	feePoolBefore, err := ts.distrKeeper.FeePool.Get(ctx)
	require.NoError(t, err)
	err = ts.distrKeeper.BeginBlocker(ctx)
	require.NoError(t, err)
	feePoolAfter, err := ts.distrKeeper.FeePool.Get(ctx)
	require.NoError(t, err)

	require.True(t, feePoolBefore.CommunityPool.Equal(feePoolAfter.CommunityPool), fmt.Sprintf("before: %s, after: %s", feePoolBefore.CommunityPool.String(), feePoolAfter.CommunityPool.String()))

	// the proposer is not recorded
	_, err = ts.distrKeeper.GetPreviousProposerConsAddr(ctx)
	require.Error(t, err)
}

// Scenario:
// multiple validators with different commission and the same stake
// after the begin blocker, rewards should be distributed evenly based on their stake weight
// commission should differ based on their differing commission rates
// community pool should have portion of rewards distributed
func TestBeginBlockToMultipleValidators(t *testing.T) {
	ts := setupTest(t)

	ctx := ts.testCtx.Ctx.
		WithBlockHeader(cmtproto.Header{
			ProposerAddress: testProposerAddress,
			Time:            time.Now(),
		})

	// reset fee pool & set params
	require.NoError(t, ts.distrKeeper.Params.Set(ctx, types.DefaultParams()))
	require.NoError(t, ts.distrKeeper.FeePool.Set(ctx, types.InitialFeePool()))

	// create validator with 50% commission
	valAddr0 := sdk.ValAddress(valConsAddr0)
	val0, err := distrtestutil.CreateValidator(valConsPk0, math.NewInt(100))
	require.NoError(t, err)
	val0.Commission = stakingtypes.NewCommission(math.LegacyNewDecWithPrec(5, 1), math.LegacyNewDecWithPrec(5, 1), math.LegacyNewDec(0))
	ts.stakingKeeper.EXPECT().ValidatorByConsAddr(gomock.Any(), sdk.GetConsAddress(valConsPk0)).Return(val0, nil).AnyTimes()

	// create second validator with 0% commission
	valAddr1 := sdk.ValAddress(valConsAddr1)
	val1, err := distrtestutil.CreateValidator(valConsPk1, math.NewInt(100))
	require.NoError(t, err)
	val1.Commission = stakingtypes.NewCommission(math.LegacyNewDec(0), math.LegacyNewDec(0), math.LegacyNewDec(0))
	ts.stakingKeeper.EXPECT().ValidatorByConsAddr(gomock.Any(), sdk.GetConsAddress(valConsPk1)).Return(val1, nil).AnyTimes()

	t.Run("assert initial state is zero", func(t *testing.T) {
		// assert initial state: zero outstanding rewards, zero community pool, zero commission, zero current rewards
		val0OutstandingRewards, err := ts.distrKeeper.GetValidatorOutstandingRewards(ctx, valAddr0)
		require.NoError(t, err)
		require.True(t, val0OutstandingRewards.Rewards.IsZero())

		val1OutstandingRewards, err := ts.distrKeeper.GetValidatorOutstandingRewards(ctx, valAddr1)
		require.NoError(t, err)
		require.True(t, val1OutstandingRewards.Rewards.IsZero())

		feePool, err := ts.distrKeeper.FeePool.Get(ctx)
		require.NoError(t, err)
		require.True(t, feePool.CommunityPool.IsZero())

		val0Commission, err := ts.distrKeeper.GetValidatorAccumulatedCommission(ctx, valAddr0)
		require.NoError(t, err)
		require.True(t, val0Commission.Commission.IsZero())

		val1Commission, err := ts.distrKeeper.GetValidatorAccumulatedCommission(ctx, valAddr1)
		require.NoError(t, err)
		require.True(t, val1Commission.Commission.IsZero())

		val0CurrentRewards, err := ts.distrKeeper.GetValidatorCurrentRewards(ctx, valAddr0)
		require.NoError(t, err)
		require.True(t, val0CurrentRewards.Rewards.IsZero())

		val1CurrentRewards, err := ts.distrKeeper.GetValidatorCurrentRewards(ctx, valAddr1)
		require.NoError(t, err)
		require.True(t, val1CurrentRewards.Rewards.IsZero())
	})

	// AllocateTokens is a no-op, so no fee-collector reads or module transfers.

	votes := []abci.VoteInfo{
		{
			Validator: abci.Validator{
				Address: valConsPk0.Address(),
				Power:   100,
			},
		},
		{
			Validator: abci.Validator{
				Address: valConsPk1.Address(),
				Power:   100,
			},
		},
	}
	ctx = ctx.WithVoteInfos(votes).WithBlockHeight(2)

	feePoolBefore, err := ts.distrKeeper.FeePool.Get(ctx)
	require.NoError(t, err)
	err = ts.distrKeeper.BeginBlocker(ctx)
	require.NoError(t, err)
	feePoolAfter, err := ts.distrKeeper.FeePool.Get(ctx)
	require.NoError(t, err)

	require.True(t, feePoolBefore.CommunityPool.Equal(feePoolAfter.CommunityPool), fmt.Sprintf("before: %s, after: %s", feePoolBefore.CommunityPool.String(), feePoolAfter.CommunityPool.String()))

	t.Run("assert no rewards or commission are distributed", func(t *testing.T) {
		val0OutstandingRewards, err := ts.distrKeeper.GetValidatorOutstandingRewards(ctx, valAddr0)
		require.NoError(t, err)
		require.True(t, val0OutstandingRewards.Rewards.IsZero())

		val1OutstandingRewards, err := ts.distrKeeper.GetValidatorOutstandingRewards(ctx, valAddr1)
		require.NoError(t, err)
		require.True(t, val1OutstandingRewards.Rewards.IsZero())

		val0Commission, err := ts.distrKeeper.GetValidatorAccumulatedCommission(ctx, valAddr0)
		require.NoError(t, err)
		require.True(t, val0Commission.Commission.IsZero())

		val1Commission, err := ts.distrKeeper.GetValidatorAccumulatedCommission(ctx, valAddr1)
		require.NoError(t, err)
		require.True(t, val1Commission.Commission.IsZero())
	})

	// the proposer is not recorded
	_, err = ts.distrKeeper.GetPreviousProposerConsAddr(ctx)
	require.Error(t, err)
}

// Scenario:
// multiple validators with same commission and the same stake
// after the begin blocker, rewards should be distributed evenly based on their stake weight
// community pool should have portion of rewards distributed - should collect dust as decimal values
func TestBeginBlockCommunityPoolCollectsDust(t *testing.T) {
	ts := setupTest(t)
	ctx := ts.testCtx.Ctx.
		WithBlockHeader(cmtproto.Header{
			ProposerAddress: testProposerAddress,
			Time:            time.Now(),
		})

	// reset fee pool
	require.NoError(t, ts.distrKeeper.FeePool.Set(ctx, types.InitialFeePool()))
	require.NoError(t, ts.distrKeeper.Params.Set(ctx, types.DefaultParams()))

	// create validator with 10% commission
	valAddr0 := sdk.ValAddress(valConsAddr0)
	val0, err := distrtestutil.CreateValidator(valConsPk0, math.NewInt(100))
	require.NoError(t, err)
	val0.Commission = stakingtypes.NewCommission(math.LegacyNewDecWithPrec(1, 1), math.LegacyNewDecWithPrec(1, 1), math.LegacyNewDec(0))
	ts.stakingKeeper.EXPECT().ValidatorByConsAddr(gomock.Any(), sdk.GetConsAddress(valConsPk0)).Return(val0, nil).AnyTimes()

	// create second validator with 10% commission
	valAddr1 := sdk.ValAddress(valConsAddr1)
	val1, err := distrtestutil.CreateValidator(valConsPk1, math.NewInt(100))
	require.NoError(t, err)
	val1.Commission = stakingtypes.NewCommission(math.LegacyNewDecWithPrec(1, 1), math.LegacyNewDecWithPrec(1, 1), math.LegacyNewDec(0))
	ts.stakingKeeper.EXPECT().ValidatorByConsAddr(gomock.Any(), sdk.GetConsAddress(valConsPk1)).Return(val1, nil).AnyTimes()

	// create third validator with 10% commission
	valAddr2 := sdk.ValAddress(valConsAddr2)
	val2, err := stakingtypes.NewValidator(sdk.ValAddress(valConsAddr2).String(), valConsPk1, stakingtypes.Description{})
	require.NoError(t, err)
	val2.Commission = stakingtypes.NewCommission(math.LegacyNewDecWithPrec(1, 1), math.LegacyNewDecWithPrec(1, 1), math.LegacyNewDec(0))
	ts.stakingKeeper.EXPECT().ValidatorByConsAddr(gomock.Any(), sdk.GetConsAddress(valConsPk2)).Return(val2, nil).AnyTimes()

	t.Run("assert initial state is zero", func(t *testing.T) {
		// assert initial state: zero outstanding rewards, zero community pool, zero commission, zero current rewards
		val0OutstandingRewards, err := ts.distrKeeper.GetValidatorOutstandingRewards(ctx, valAddr0)
		require.NoError(t, err)
		require.True(t, val0OutstandingRewards.Rewards.IsZero())

		val1OutstandingRewards, err := ts.distrKeeper.GetValidatorOutstandingRewards(ctx, valAddr1)
		require.NoError(t, err)
		require.True(t, val1OutstandingRewards.Rewards.IsZero())

		val2OutstandingRewards, err := ts.distrKeeper.GetValidatorOutstandingRewards(ctx, valAddr2)
		require.NoError(t, err)
		require.True(t, val2OutstandingRewards.Rewards.IsZero())

		feePool, err := ts.distrKeeper.FeePool.Get(ctx)
		require.NoError(t, err)
		require.True(t, feePool.CommunityPool.IsZero())

		val0Commission, err := ts.distrKeeper.GetValidatorAccumulatedCommission(ctx, valAddr0)
		require.NoError(t, err)
		require.True(t, val0Commission.Commission.IsZero())

		val1Commission, err := ts.distrKeeper.GetValidatorAccumulatedCommission(ctx, valAddr1)
		require.NoError(t, err)
		require.True(t, val1Commission.Commission.IsZero())

		val0CurrentRewards, err := ts.distrKeeper.GetValidatorCurrentRewards(ctx, valAddr0)
		require.NoError(t, err)
		require.True(t, val0CurrentRewards.Rewards.IsZero())

		val1CurrentRewards, err := ts.distrKeeper.GetValidatorCurrentRewards(ctx, valAddr1)
		require.NoError(t, err)
		require.True(t, val1CurrentRewards.Rewards.IsZero())
	})

	// AllocateTokens is a no-op, so dust is not swept into the community pool.

	votes := []abci.VoteInfo{
		{
			Validator: abci.Validator{
				Address: valConsPk0.Address(),
				Power:   11,
			},
		},
		{
			Validator: abci.Validator{
				Address: valConsPk1.Address(),
				Power:   10,
			},
		},
		{
			Validator: abci.Validator{
				Address: valConsPk2.Address(),
				Power:   10,
			},
		},
	}
	ctx = ctx.WithVoteInfos(votes).WithBlockHeight(2)

	feePoolBefore, err := ts.distrKeeper.FeePool.Get(ctx)
	require.NoError(t, err)
	err = ts.distrKeeper.BeginBlocker(ctx)
	require.NoError(t, err)
	feePoolAfter, err := ts.distrKeeper.FeePool.Get(ctx)
	require.NoError(t, err)

	require.True(t, feePoolBefore.CommunityPool.Equal(feePoolAfter.CommunityPool), fmt.Sprintf("before: %s, after: %s", feePoolBefore.CommunityPool.String(), feePoolAfter.CommunityPool.String()))

	t.Run("assert rewards distributed", func(t *testing.T) {
		val0OutstandingRewards, err := ts.distrKeeper.GetValidatorOutstandingRewards(ctx, valAddr0)
		require.NoError(t, err)
		require.True(t, val0OutstandingRewards.Rewards.IsValid())

		val1OutstandingRewards, err := ts.distrKeeper.GetValidatorOutstandingRewards(ctx, valAddr1)
		require.NoError(t, err)
		require.True(t, val1OutstandingRewards.Rewards.IsValid())

		val2OutstandingRewards, err := ts.distrKeeper.GetValidatorOutstandingRewards(ctx, valAddr2)
		require.NoError(t, err)
		require.True(t, val2OutstandingRewards.Rewards.IsValid())
	})

	// the proposer is not recorded
	_, err = ts.distrKeeper.GetPreviousProposerConsAddr(ctx)
	require.Error(t, err)
}
