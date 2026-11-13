package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// initializeValidator initializes rewards for a new validator
func (k Keeper) initializeValidator(ctx context.Context, val stakingtypes.ValidatorI) error {
	valBz, err := k.stakingKeeper.ValidatorAddressCodec().StringToBytes(val.GetOperator())
	if err != nil {
		return err
	}
	// set initial historical rewards (period 0) with reference count of 1
	err = k.SetValidatorHistoricalRewards(ctx, valBz, 0, types.NewValidatorHistoricalRewards(sdk.DecCoins{}, 1))
	if err != nil {
		return err
	}

	// set current rewards (starting at period 1)
	err = k.SetValidatorCurrentRewards(ctx, valBz, types.NewValidatorCurrentRewards(sdk.DecCoins{}, 1))
	if err != nil {
		return err
	}

	// set accumulated commission
	err = k.SetValidatorAccumulatedCommission(ctx, valBz, types.InitialValidatorAccumulatedCommission())
	if err != nil {
		return err
	}

	// set outstanding rewards
	err = k.SetValidatorOutstandingRewards(ctx, valBz, types.ValidatorOutstandingRewards{Rewards: sdk.DecCoins{}})
	return err
}

func (k Keeper) IncrementValidatorPeriod(ctx context.Context, val stakingtypes.ValidatorI) (uint64, error) {
	return 0, nil
}

func (k Keeper) incrementReferenceCount(ctx context.Context, valAddr sdk.ValAddress, period uint64) error {
	return nil
}

func (k Keeper) decrementReferenceCount(ctx context.Context, valAddr sdk.ValAddress, period uint64) error {
	return nil
}

func (k Keeper) updateValidatorSlashFraction(ctx context.Context, valAddr sdk.ValAddress, fraction math.LegacyDec) error {
	if fraction.GT(math.LegacyOneDec()) || fraction.IsNegative() {
		panic(fmt.Sprintf("fraction must be >=0 and <=1, current fraction: %v", fraction))
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	val, err := k.stakingKeeper.Validator(ctx, valAddr)
	if err != nil {
		return err
	}

	// increment current period
	newPeriod, err := k.IncrementValidatorPeriod(ctx, val)
	if err != nil {
		return err
	}

	// increment reference count on period we need to track
	err = k.incrementReferenceCount(ctx, valAddr, newPeriod)
	if err != nil {
		return err
	}

	slashEvent := types.NewValidatorSlashEvent(newPeriod, fraction)
	height := uint64(sdkCtx.BlockHeight())

	return k.SetValidatorSlashEvent(ctx, valAddr, height, newPeriod, slashEvent)
}
