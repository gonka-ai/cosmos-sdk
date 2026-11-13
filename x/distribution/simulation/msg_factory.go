package simulation

import (
	"context"

	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/testutil/simsx"
	"github.com/cosmos/cosmos-sdk/x/distribution/keeper"
	"github.com/cosmos/cosmos-sdk/x/distribution/types"
)

func MsgSetWithdrawAddressFactory(k keeper.Keeper) simsx.SimMsgFactoryFn[*types.MsgSetWithdrawAddress] {
	return func(ctx context.Context, testData *simsx.ChainDataSource, reporter simsx.SimulationReporter) ([]simsx.SimAccount, *types.MsgSetWithdrawAddress) {
		reporter.Skip("SetWithdrawAddress is disabled")
		return nil, nil
	}
}

func MsgWithdrawDelegatorRewardFactory(k keeper.Keeper, sk types.StakingKeeper) simsx.SimMsgFactoryFn[*types.MsgWithdrawDelegatorReward] {
	return func(ctx context.Context, testData *simsx.ChainDataSource, reporter simsx.SimulationReporter) ([]simsx.SimAccount, *types.MsgWithdrawDelegatorReward) {
		reporter.Skip("WithdrawDelegatorReward is disabled")
		return nil, nil
	}
}

func MsgWithdrawValidatorCommissionFactory(k keeper.Keeper, sk types.StakingKeeper) simsx.SimMsgFactoryFn[*types.MsgWithdrawValidatorCommission] {
	return func(ctx context.Context, testData *simsx.ChainDataSource, reporter simsx.SimulationReporter) ([]simsx.SimAccount, *types.MsgWithdrawValidatorCommission) {
		reporter.Skip("WithdrawValidatorCommission is disabled")
		return nil, nil
	}
}

func MsgUpdateParamsFactory() simsx.SimMsgFactoryFn[*types.MsgUpdateParams] {
	return func(_ context.Context, testData *simsx.ChainDataSource, reporter simsx.SimulationReporter) ([]simsx.SimAccount, *types.MsgUpdateParams) {
		r := testData.Rand()
		params := types.DefaultParams()
		params.CommunityTax = r.DecN(sdkmath.LegacyNewDec(1))
		params.WithdrawAddrEnabled = r.Intn(2) == 0

		return nil, &types.MsgUpdateParams{
			Authority: testData.ModuleAccountAddress(reporter, "gov"),
			Params:    params,
		}
	}
}
