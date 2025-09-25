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
	resultsMap := make(map[string]ComputeResult)
	for _, result := range computeResults {
		resultsMap[result.ValidatorPubKey.String()] = result
	}

	currentValidators, err := k.GetAllValidators(ctx)
	if err != nil {
		logger.Error("error getting current validators", "error", err.Error())
		return nil, err
	}

	validatorsAlreadyExisting := make(map[string]bool)
	for _, validator := range currentValidators {
		conPubKey, err := validator.ConsPubKey()
		if err != nil {
			logger.Error("Error getting cons pubkey", "error", err.Error())
			return nil, err
		}
		validatorsAlreadyExisting[conPubKey.String()] = true
	}

	// Handle validators already in
	for _, validator := range currentValidators {
		conPubKey, err := validator.ConsPubKey()
		if err != nil {
			logger.Error("Error getting cons pubkey", "error", err.Error())
			return nil, err
		}
		computeResult, inNewResults := resultsMap[conPubKey.String()]
		if inNewResults {
			logger.Info("Updating validator", "operator", validator.GetOperator(), "power", computeResult.Power)
			_, err := k.updateValidatorFromComputeResults(ctx, validator, computeResult)
			if err != nil {
				return nil, err
			}
		} else {
			logger.Info("Removing validator", "operator", validator.GetOperator(), "power", computeResult.Power)
			computeResult.Power = 0
			_, err := k.updateValidatorFromComputeResults(ctx, validator, computeResult)
			if err != nil {
				return nil, err
			}
		}
	}
	// Handle validators not in
	for _, computeResult := range computeResults {
		if computeResult.Power == 0 {
			logger.Warn("Power is 0 for new validator, skipping validator", "address", computeResult.OperatorAddress, "key", computeResult.ValidatorPubKey.String())
			continue
		}
		if _, ok := validatorsAlreadyExisting[computeResult.ValidatorPubKey.String()]; !ok {
			logger.Info("Creating validator", "power", computeResult, "operator", computeResult.OperatorAddress)
			newVal, err := k.createValidatorFromComputeResult(ctx, computeResult)
			if err != nil {
				logger.Error("Error creating validator", "error", err.Error())
				return nil, err
			}
			return append(currentValidators, *newVal), nil
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

		types.CommissionRates{
			Rate:          math.LegacyMustNewDecFromStr("0.1"),
			MaxRate:       math.LegacyMustNewDecFromStr("0.2"),
			MaxChangeRate: math.LegacyMustNewDecFromStr("0.01"),
		},
		math.NewInt(1),
	)
	if err != nil {
		logger.Error("Error creating validator message", "error", err.Error())
		return nil, err
	}
	_, err = k.CreateComputeValidator(ctx, createValidatorMsg)
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

func (k Keeper) updateValidatorFromComputeResults(ctx context.Context, validator types.Validator, computeResult ComputeResult) (types.Validator, error) {
	logger := k.Logger(ctx)
	power := validator.Tokens.Int64()
	err := k.DeleteComputeValidatorByPowerIndex(ctx, validator)
	if err != nil {
		logger.Error("Error deleting validator by power index", "error", err.Error())
		return validator, err
	}

	validator.Tokens = math.NewInt(power)
	err = k.SetComputeValidator(ctx, validator)
	if err != nil {
		logger.Error("Error setting validator", "error", err.Error())
		return validator, err
	}
	err = k.SetComputeValidatorByPowerIndex(ctx, validator)
	if err != nil {
		logger.Error("Error setting validator by power index", "error", err.Error())
		return validator, err
	}
	return validator, nil
}

func (k Keeper) CreateComputeValidator(ctx context.Context, msg *types.MsgCreateValidator) (*types.MsgCreateValidatorResponse, error) {
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

	err = k.SetComputeValidator(ctx, validator)
	if err != nil {
		return nil, err
	}

	err = k.SetComputeValidatorByConsAddr(ctx, validator)
	if err != nil {
		return nil, err
	}

	err = k.SetNewComputeValidatorByPowerIndex(ctx, validator)
	if err != nil {
		return nil, err
	}

	// call the after-creation hook
	if err := k.Hooks().AfterValidatorCreated(ctx, valAddr); err != nil {
		return nil, err
	}

	_, err = k.ComputeDelegate(ctx, sdk.AccAddress(valAddr), msg.Value.Amount, types.Unbonded, validator, true)
	if err != nil {
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
