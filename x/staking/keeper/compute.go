package keeper

import (
	"context"
	"slices"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

type ComputeResult struct {
	Power           int64
	ValidatorPubKey cryptotypes.PubKey
	OperatorAddress string
}

func (k Keeper) SetComputeValidators(ctx context.Context, computeResults []ComputeResult) ([]types.Validator, error) {
	logger := k.Logger(ctx)

	resultsMap := k.filterValidComputeResults(ctx, computeResults)
	if len(resultsMap) == 0 {
		logger.Warn("No valid compute results after filtering")
		return k.GetAllValidators(ctx)
	}

	currentValidators, err := k.GetAllValidators(ctx)
	if err != nil {
		logger.Error("error getting current validators", "error", err.Error())
		return nil, err
	}

	valToUpdate := make([]types.Validator, 0)
	valToRemove := make([]types.Validator, 0)
	foundValidators := make(map[string]bool)

	for _, validator := range currentValidators {
		conPubKey, err := validator.ConsPubKey()
		if err != nil || conPubKey == nil {
			logger.Warn("Invalid validator consensus pubkey, will remove", "operator", validator.GetOperator())
			valToRemove = append(valToRemove, validator)
			continue
		}

		foundValidators[conPubKey.String()] = true
		if _, found := resultsMap[conPubKey.String()]; !found || resultsMap[conPubKey.String()].Power == 0 {
			valToRemove = append(valToRemove, validator)
		} else {
			valToUpdate = append(valToUpdate, validator)
		}
	}

	for _, result := range resultsMap {
		if _, found := foundValidators[result.ValidatorPubKey.String()]; found {
			continue
		}

		_, err := k.createValidatorFromComputeResult(ctx, result)
		if err != nil {
			logger.Error("Error creating validator from compute result", "error", err.Error())
			continue
		}
	}

	for _, validator := range valToUpdate {
		conPubKey, err := validator.ConsPubKey()
		if err != nil {
			logger.Error("Error getting consensus pubkey, skipping", "operator", validator.GetOperator(), "error", err.Error())
			continue
		}
		computeResult := resultsMap[conPubKey.String()]
		err = k.updateValidatorPower(ctx, validator, computeResult.Power)
		if err != nil {
			logger.Error("Error updating validator, skipping", "operator", validator.GetOperator(), "error", err.Error())
			continue
		}
	}

	for _, validator := range valToRemove {
		err = k.removeValidator(ctx, validator)
		if err != nil {
			logger.Error("Error removing validator, skipping", "operator", validator.GetOperator(), "error", err.Error())
			continue
		}
	}

	return k.GetAllValidators(ctx)
}

func (k Keeper) createValidatorFromComputeResult(ctx context.Context, computeResult ComputeResult) (*types.Validator, error) {
	logger := k.Logger(ctx)
	newValAddr, err := sdk.ValAddressFromBech32(computeResult.OperatorAddress)

	if err != nil {
		logger.Error("Error converting operator address to val address", "error", err.Error())
		return nil, err
	}
	denom, err := k.BondDenom(ctx)
	if err != nil {
		return nil, err
	}

	createValidatorMsg, err := types.NewMsgCreateValidator(
		newValAddr.String(),
		computeResult.ValidatorPubKey,
		sdk.NewCoin(denom, math.NewInt(computeResult.Power)),
		types.Description{
			Moniker: newValAddr.String(),
			Details: "Created after Proof of Compute",
		},

		types.CommissionRates{},
		math.NewInt(computeResult.Power),
	)
	if err != nil {
		logger.Error("Error creating validator message", "error", err.Error())
		return nil, err
	}
	_, err = k.createComputeValidator(ctx, createValidatorMsg)
	if err != nil {
		logger.Error("Error creating validator", "error", err.Error())
		return nil, err
	}
	newVal, err := k.GetValidator(ctx, newValAddr)
	if err != nil {
		logger.Error("Error getting created validator", "error", err.Error())
		return nil, err
	}
	logger.Info("Created validator", "validator", newVal.String())
	bondedVal, err := k.bondComputeValidator(ctx, newVal)
	if err != nil {
		logger.Error("Error bonding validator", "error", err.Error())
		return nil, err
	}
	logger.Info("Bonded validator", "validator", bondedVal.String())

	valAddr, err := sdk.ValAddressFromBech32(computeResult.OperatorAddress)
	if err != nil {
		k.Logger(ctx).Error("Error parsing operator address", "address", computeResult.OperatorAddress, "error", err)
		return nil, err
	}

	delegatorAccountAddress := sdk.AccAddress(valAddr)

	delegation := types.Delegation{
		DelegatorAddress: delegatorAccountAddress.String(),
		ValidatorAddress: computeResult.OperatorAddress,
	}

	delegation.Shares = math.LegacyNewDec(computeResult.Power)

	err = k.SetComputeDelegation(ctx, delegation)
	if err != nil {
		k.Logger(ctx).Error("Error setting delegation", "error", err.Error())
		return nil, err
	}

	return &bondedVal, nil
}

func (k Keeper) removeValidator(ctx context.Context, validator types.Validator) error {
	logger := k.Logger(ctx)

	valAddr, err := k.ValidatorAddressCodec().StringToBytes(validator.GetOperator())
	if err != nil {
		logger.Error("Failed to parse validator address", "operator", validator.GetOperator(), "error", err.Error())
		return err
	}

	logger.Info("Removing validator", "operator", validator.GetOperator())

	addr := sdk.AccAddress(valAddr)
	_, err = k.SetCompute(ctx, addr, math.NewInt(0), validator)
	if err != nil {
		logger.Error("Failed to remove validator", "operator", validator.GetOperator(), "error", err.Error())
		return err
	}

	logger.Info("Successfully removed validator", "operator", validator.GetOperator())
	return nil
}

func (k Keeper) createComputeValidator(ctx context.Context, msg *types.MsgCreateValidator) (*types.MsgCreateValidatorResponse, error) {
	valAddr, err := k.validatorAddressCodec.StringToBytes(msg.ValidatorAddress)
	if err != nil {
		return nil, sdkerrors.ErrInvalidAddress.Wrapf("invalid validator address: %s", err)
	}

	if err := msg.Validate(k.validatorAddressCodec); err != nil {
		return nil, err
	}

	minCommRate, err := k.MinCommissionRate(ctx)
	if err != nil {
		return nil, err
	}

	if msg.Commission.Rate.LT(minCommRate) {
		return nil, errorsmod.Wrapf(types.ErrCommissionLTMinRate, "cannot set validator commission to less than minimum rate of %s", minCommRate)
	}

	// check to see if the pubkey or sender has been registered before
	if _, err := k.GetValidator(ctx, valAddr); err == nil {
		return nil, types.ErrValidatorOwnerExists
	}

	pk, ok := msg.Pubkey.GetCachedValue().(cryptotypes.PubKey)
	if !ok {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidType, "Expecting cryptotypes.PubKey, got %T", pk)
	}

	// Validate the public key to ensure it won't cause panics
	if err := safeValidatePublicKey(pk); err != nil {
		return nil, err
	}

	if _, err := k.GetValidatorByConsAddr(ctx, sdk.GetConsAddress(pk)); err == nil {
		return nil, types.ErrValidatorPubKeyExists
	}

	bondDenom, err := k.BondDenom(ctx)
	if err != nil {
		return nil, err
	}

	if msg.Value.Denom != bondDenom {
		return nil, errorsmod.Wrapf(
			sdkerrors.ErrInvalidRequest, "invalid coin denomination: got %s, expected %s", msg.Value.Denom, bondDenom,
		)
	}

	if _, err := msg.Description.EnsureLength(); err != nil {
		return nil, err
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cp := sdkCtx.ConsensusParams()
	if cp.Validator != nil {
		pkType := pk.Type()
		hasKeyType := slices.Contains(cp.Validator.PubKeyTypes, pkType)
		if !hasKeyType {
			return nil, errorsmod.Wrapf(
				types.ErrValidatorPubKeyTypeNotSupported,
				"got: %s, expected: %s", pk.Type(), cp.Validator.PubKeyTypes,
			)
		}
	}

	validator, err := types.NewValidator(msg.ValidatorAddress, pk, msg.Description)
	if err != nil {
		return nil, err
	}

	commission := types.NewCommissionWithTime(
		msg.Commission.Rate, msg.Commission.MaxRate,
		msg.Commission.MaxChangeRate, sdkCtx.BlockHeader().Time,
	)

	validator, err = validator.SetInitialCommission(commission)
	if err != nil {
		return nil, err
	}

	validator.MinSelfDelegation = msg.MinSelfDelegation

	// Validate token amount is positive
	if msg.Value.Amount.IsZero() {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "validator initial stake cannot be zero")
	}
	if msg.Value.Amount.IsNegative() {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "validator initial stake cannot be negative")
	}

	// Set initial tokens and delegator shares for compute validators
	validator.Tokens = msg.Value.Amount
	validator.DelegatorShares = math.LegacyNewDecFromInt(msg.Value.Amount)

	// Clear unbonding IDs since there's no unbonding in Proof of Compute
	validator.UnbondingIds = []uint64{}

	err = k.SetComputeValidator(ctx, validator)
	if err != nil {
		return nil, err
	}

	err = k.SetComputeValidatorByConsAddr(ctx, validator)
	if err != nil {
		return nil, err
	}

	// call the after-creation hook
	if err := k.Hooks().AfterValidatorCreated(ctx, valAddr); err != nil {
		return nil, err
	}

	sdkCtx.EventManager().EmitEvents(sdk.Events{
		sdk.NewEvent(
			types.EventTypeCreateValidator,
			sdk.NewAttribute(types.AttributeKeyValidator, msg.ValidatorAddress),
			sdk.NewAttribute(sdk.AttributeKeyAmount, msg.Value.String()),
		),
	})

	return &types.MsgCreateValidatorResponse{}, nil
}

func (k Keeper) SetCompute(
	ctx context.Context, delAddr sdk.AccAddress, power math.Int,
	validator types.Validator,
) (newShares math.LegacyDec, err error) {
	logger := k.Logger(ctx)

	if validator.InvalidExRate() {
		logger.Error("Invalid delegation share exchange rate", "validator", validator.GetOperator())
		return math.LegacyZeroDec(), types.ErrDelegatorShareExRateInvalid
	}

	if err := k.DeleteComputeValidatorByPowerIndex(ctx, validator); err != nil {
		logger.Error("Error deleting validator by power index", "validator", validator.GetOperator(), "error", err.Error())
		return math.LegacyZeroDec(), err
	}

	if power.IsZero() || power.IsNegative() {
		logger.Warn("Power is zero or negative, skipping", "validator", validator.GetOperator(), "power", power)
		return math.LegacyZeroDec(), nil
	}

	valbz, err := k.ValidatorAddressCodec().StringToBytes(validator.GetOperator())
	if err != nil {
		logger.Error("Error getting validator address", "validator", validator.GetOperator(), "error", err.Error())
		return math.LegacyZeroDec(), err
	}

	delegation, err := k.GetDelegation(ctx, delAddr, valbz)
	if err != nil {
		logger.Error("Error getting delegation", "validator", validator.GetOperator(), "error", err.Error())
		return math.LegacyZeroDec(), err
	}

	k.RemoveComputeDelegation(ctx, delegation)

	validator.Status = types.Bonded
	validator.Jailed = false
	validator.Tokens = power
	validator.DelegatorShares = math.LegacyNewDecFromInt(power)

	validator.UnbondingIds = []uint64{}
	delegation.Shares = math.LegacyNewDecFromInt(power)

	if err = k.SetComputeValidator(ctx, validator); err != nil {
		logger.Error("Error setting validator", "validator", validator.GetOperator(), "error", err.Error())
		return math.LegacyDec{}, err
	}

	if err = k.SetComputeValidatorByPowerIndex(ctx, validator); err != nil {
		logger.Error("Error setting validator by power index", "validator", validator.GetOperator(), "error", err.Error())
		return math.LegacyDec{}, err
	}

	if err = k.SetComputeDelegation(ctx, delegation); err != nil {
		logger.Error("Error setting delegation", "validator", validator.GetOperator(), "error", err.Error())
		return newShares, err
	}

	if err := k.Hooks().AfterDelegationModified(ctx, delAddr, valbz); err != nil {
		k.Logger(ctx).Error("Error in after delegation modified hook", "error", err.Error())
		return math.LegacyZeroDec(), err
	}

	// Call the after-bonded hook to ensure signing info exists
	consAddr, err := validator.GetConsAddr()
	if err != nil {
		logger.Error("Error getting consensus address", "validator", validator.GetOperator(), "error", err.Error())
		return math.LegacyDec{}, err
	}
	if err := k.Hooks().AfterValidatorBonded(ctx, consAddr, valbz); err != nil {
		k.Logger(ctx).Error("Error in after validator bonded hook", "error", err.Error())
		return math.LegacyDec{}, err
	}

	return delegation.Shares, nil
}

func (k Keeper) filterValidComputeResults(ctx context.Context, computeResults []ComputeResult) map[string]ComputeResult {
	logger := k.Logger(ctx)

	if len(computeResults) == 0 {
		return nil
	}

	validResults := make(map[string]ComputeResult)

	for i, result := range computeResults {
		if result.ValidatorPubKey == nil {
			logger.Warn("Nil ValidatorPubKey, skipping", "index", i)
			continue
		}
		if err := safeValidatePublicKey(result.ValidatorPubKey); err != nil {
			logger.Warn("Invalid ValidatorPubKey, skipping", "index", i, "error", err.Error())
			continue
		}

		if result.OperatorAddress == "" {
			logger.Warn("Empty OperatorAddress, skipping", "index", i)
			continue
		}

		if _, err := sdk.ValAddressFromBech32(result.OperatorAddress); err != nil {
			logger.Warn("Invalid OperatorAddress format, skipping", "index", i, "address", result.OperatorAddress)
			continue
		}

		if result.Power < 0 {
			logger.Warn("Negative power, skipping", "index", i, "power", result.Power)
			continue
		}

		pubKeyStr := result.ValidatorPubKey.String()
		if _, found := validResults[pubKeyStr]; found {
			logger.Warn("Duplicate pubkey, skipping", "index", i, "pubkey", pubKeyStr)
			continue
		}
		validResults[pubKeyStr] = result

	}

	logger.Debug("Filtered compute results", "original", len(computeResults), "valid", len(validResults))
	return validResults
}

// updateValidatorPower updates validator power using SetCompute
func (k Keeper) updateValidatorPower(ctx context.Context, validator types.Validator, power int64) error {
	valAddr, err := sdk.ValAddressFromBech32(validator.GetOperator())
	if err != nil {
		return err
	}

	addr := sdk.AccAddress(valAddr)

	// SetCompute handles power index management internally
	_, err = k.SetCompute(ctx, addr, math.NewInt(power), validator)
	return err
}
