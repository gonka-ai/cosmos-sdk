package simulation

import (
	"math/rand"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	simtypes "github.com/cosmos/cosmos-sdk/types/simulation"
	"github.com/cosmos/cosmos-sdk/x/distribution/keeper"
	"github.com/cosmos/cosmos-sdk/x/distribution/types"
	"github.com/cosmos/cosmos-sdk/x/simulation"
)

// Simulation operation weights constants
// will be removed in the future
const (
	OpWeightMsgSetWithdrawAddress          = "op_weight_msg_set_withdraw_address"
	OpWeightMsgWithdrawDelegationReward    = "op_weight_msg_withdraw_delegation_reward"
	OpWeightMsgWithdrawValidatorCommission = "op_weight_msg_withdraw_validator_commission"
	OpWeightMsgFundCommunityPool           = "op_weight_msg_fund_community_pool"

	DefaultWeightMsgSetWithdrawAddress          int = 50
	DefaultWeightMsgWithdrawDelegationReward    int = 50
	DefaultWeightMsgWithdrawValidatorCommission int = 50
	DefaultWeightMsgFundCommunityPool           int = 50
)

// WeightedOperations returns all the operations from the module with their respective weights
// migrate to the msg factories instead, this method will be removed in the future
func WeightedOperations(
	appParams simtypes.AppParams,
	_ codec.JSONCodec,
	txConfig client.TxConfig,
	ak types.AccountKeeper,
	bk types.BankKeeper,
	k keeper.Keeper,
	sk types.StakingKeeper,
) simulation.WeightedOperations {
	var weightMsgSetWithdrawAddress int
	appParams.GetOrGenerate(OpWeightMsgSetWithdrawAddress, &weightMsgSetWithdrawAddress, nil, func(_ *rand.Rand) {
		weightMsgSetWithdrawAddress = DefaultWeightMsgSetWithdrawAddress
	})

	var weightMsgWithdrawDelegationReward int
	appParams.GetOrGenerate(OpWeightMsgWithdrawDelegationReward, &weightMsgWithdrawDelegationReward, nil, func(_ *rand.Rand) {
		weightMsgWithdrawDelegationReward = DefaultWeightMsgWithdrawDelegationReward
	})

	var weightMsgWithdrawValidatorCommission int
	appParams.GetOrGenerate(OpWeightMsgWithdrawValidatorCommission, &weightMsgWithdrawValidatorCommission, nil, func(_ *rand.Rand) {
		weightMsgWithdrawValidatorCommission = DefaultWeightMsgWithdrawValidatorCommission
	})

	var weightMsgFundCommunityPool int
	appParams.GetOrGenerate(OpWeightMsgFundCommunityPool, &weightMsgFundCommunityPool, nil, func(_ *rand.Rand) {
		weightMsgFundCommunityPool = DefaultWeightMsgFundCommunityPool
	})

	ops := simulation.WeightedOperations{
		simulation.NewWeightedOperation(
			weightMsgSetWithdrawAddress,
			SimulateMsgSetWithdrawAddress(txConfig, ak, bk, k),
		),
		simulation.NewWeightedOperation(
			weightMsgWithdrawDelegationReward,
			SimulateMsgWithdrawDelegatorReward(txConfig, ak, bk, k, sk),
		),
		simulation.NewWeightedOperation(
			weightMsgWithdrawValidatorCommission,
			SimulateMsgWithdrawValidatorCommission(txConfig, ak, bk, k, sk),
		),
		simulation.NewWeightedOperation(
			weightMsgFundCommunityPool,
			SimulateMsgFundCommunityPool(txConfig, ak, bk, k, sk),
		),
	}

	return ops
}

// SimulateMsgSetWithdrawAddress generates a MsgSetWithdrawAddress with random values.
// migrate to the msg factories instead, this method will be removed in the future
func SimulateMsgSetWithdrawAddress(txConfig client.TxConfig, ak types.AccountKeeper, bk types.BankKeeper, k keeper.Keeper) simtypes.Operation {
	return func(
		r *rand.Rand, app *baseapp.BaseApp, ctx sdk.Context, accs []simtypes.Account, chainID string,
	) (simtypes.OperationMsg, []simtypes.FutureOperation, error) {
		return simtypes.NoOpMsg(types.ModuleName, sdk.MsgTypeURL(&types.MsgSetWithdrawAddress{}), "SetWithdrawAddress is disabled"), nil, nil
	}
}

// SimulateMsgWithdrawDelegatorReward does not deliver a reward withdrawal.
// migrate to the msg factories instead, this method will be removed in the future
func SimulateMsgWithdrawDelegatorReward(txConfig client.TxConfig, ak types.AccountKeeper, bk types.BankKeeper, k keeper.Keeper, sk types.StakingKeeper) simtypes.Operation {
	return func(
		r *rand.Rand, app *baseapp.BaseApp, ctx sdk.Context, accs []simtypes.Account, chainID string,
	) (simtypes.OperationMsg, []simtypes.FutureOperation, error) {
		return simtypes.NoOpMsg(types.ModuleName, sdk.MsgTypeURL(&types.MsgWithdrawDelegatorReward{}), "WithdrawDelegatorReward is disabled"), nil, nil
	}
}

// SimulateMsgWithdrawValidatorCommission does not deliver a commission withdrawal.
// migrate to the msg factories instead, this method will be removed in the future
func SimulateMsgWithdrawValidatorCommission(txConfig client.TxConfig, ak types.AccountKeeper, bk types.BankKeeper, k keeper.Keeper, sk types.StakingKeeper) simtypes.Operation {
	return func(
		r *rand.Rand, app *baseapp.BaseApp, ctx sdk.Context, accs []simtypes.Account, chainID string,
	) (simtypes.OperationMsg, []simtypes.FutureOperation, error) {
		return simtypes.NoOpMsg(types.ModuleName, sdk.MsgTypeURL(&types.MsgWithdrawValidatorCommission{}), "WithdrawValidatorCommission is disabled"), nil, nil
	}
}

// SimulateMsgFundCommunityPool simulates MsgFundCommunityPool execution where
// a random account sends a random amount of its funds to the community pool.
// migrate to the msg factories instead, this method will be removed in the future
func SimulateMsgFundCommunityPool(txConfig client.TxConfig, ak types.AccountKeeper, bk types.BankKeeper, k keeper.Keeper, sk types.StakingKeeper) simtypes.Operation {
	return func(
		r *rand.Rand, app *baseapp.BaseApp, ctx sdk.Context, accs []simtypes.Account, chainID string,
	) (simtypes.OperationMsg, []simtypes.FutureOperation, error) {
		funder, _ := simtypes.RandomAcc(r, accs)

		account := ak.GetAccount(ctx, funder.Address)
		spendable := bk.SpendableCoins(ctx, account.GetAddress())

		fundAmount := simtypes.RandSubsetCoins(r, spendable)
		if fundAmount.Empty() {
			return simtypes.NoOpMsg(types.ModuleName, sdk.MsgTypeURL(&types.MsgFundCommunityPool{}), "fund amount is empty"), nil, nil
		}

		var (
			fees sdk.Coins
			err  error
		)

		coins, hasNeg := spendable.SafeSub(fundAmount...)
		if !hasNeg {
			fees, err = simtypes.RandomFees(r, ctx, coins)
			if err != nil {
				return simtypes.NoOpMsg(types.ModuleName, sdk.MsgTypeURL(&types.MsgFundCommunityPool{}), "unable to generate fees"), nil, err
			}
		}

		msg := types.NewMsgFundCommunityPool(fundAmount, funder.Address.String())

		txCtx := simulation.OperationInput{
			R:             r,
			App:           app,
			TxGen:         txConfig,
			Cdc:           nil,
			Msg:           msg,
			Context:       ctx,
			SimAccount:    funder,
			AccountKeeper: ak,
			ModuleName:    types.ModuleName,
		}

		return simulation.GenAndDeliverTx(txCtx, fees)
	}
}
