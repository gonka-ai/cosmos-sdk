package keeper

import (
	"context"
	"errors"
	"slices"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
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

func (k Keeper) updateValidatorFromComputeResults(ctx context.Context, validator types.Validator, computeResult ComputeResult) (types.Validator, error) {
	logger := k.Logger(ctx)
	power := computeResult.Power
	valAddr, err := sdk.ValAddressFromBech32(computeResult.OperatorAddress)
	if err != nil {
		logger.Error("Error parsing operator address as valaddress", "address", computeResult.OperatorAddress, "error", err)
		return validator, err
	}
	addr := sdk.AccAddress(valAddr)

	err = k.DeleteComputeValidatorByPowerIndex(ctx, validator)
	if err != nil {
		logger.Error("Error deleting validator by power index", "error", err.Error())
		return validator, err
	}

	k.SetCompute(ctx, addr, math.NewInt(power), validator)
	validator, err = k.GetValidator(ctx, valAddr)
	if err != nil {
		logger.Error("Error getting validator", "error", err.Error())
	}
	err = k.SetComputeValidatorByPowerIndex(ctx, validator)
	if err != nil {
		logger.Error("Error setting validator by power index", "error", err.Error())
		return validator, err
	}

	return validator, nil
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

	err = k.SetNewComputeValidatorByPowerIndex(ctx, validator)
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
	// In some situations, the exchange rate becomes invalid, e.g. if
	// Validator loses all tokens due to slashing. In this case,
	// make all future delegations invalid.
	if validator.InvalidExRate() {
		return math.LegacyZeroDec(), types.ErrDelegatorShareExRateInvalid
	}

	valbz, err := k.ValidatorAddressCodec().StringToBytes(validator.GetOperator())
	if err != nil {
		return math.LegacyZeroDec(), err
	}

	// Get or create the delegation object and call the appropriate hook if present
	delegation, err := k.GetDelegation(ctx, delAddr, valbz)
	if err == nil {
		// found
		err = k.Hooks().BeforeDelegationSharesModified(ctx, delAddr, valbz)
	} else if errors.Is(err, types.ErrNoDelegation) {
		// not found
		delAddrStr, err1 := k.authKeeper.AddressCodec().BytesToString(delAddr)
		if err1 != nil {
			return math.LegacyDec{}, err1
		}

		delegation = types.NewDelegation(delAddrStr, validator.GetOperator(), math.LegacyZeroDec())
		err = k.Hooks().BeforeDelegationCreated(ctx, delAddr, valbz)
	} else {
		return math.LegacyZeroDec(), err
	}

	if err != nil {
		return math.LegacyZeroDec(), err
	}

	// Validate power is not negative
	if power.IsNegative() {
		return math.LegacyZeroDec(), errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "power cannot be negative")
	}

	// Handle zero power - remove validator entirely in Proof of Compute
	if power.IsZero() {
		// Remove validator with zero power
		if err = k.RemoveComputeValidator(ctx, valbz); err != nil {
			return math.LegacyZeroDec(), err
		}
		// Remove delegation as well
		if err = k.RemoveComputeDelegation(ctx, delegation); err != nil {
			return math.LegacyZeroDec(), err
		}
		// Call the after-removal hook
		if err := k.Hooks().AfterDelegationModified(ctx, delAddr, valbz); err != nil {
			return math.LegacyZeroDec(), err
		}
		return math.LegacyZeroDec(), nil
	}

	// Set validator tokens and shares for non-zero power
	validator.Status = types.Bonded
	validator.Jailed = false
	validator.Tokens = power
	validator.DelegatorShares = math.LegacyNewDecFromInt(power)

	// Clear unbonding IDs since there's no unbonding in Proof of Compute
	validator.UnbondingIds = []uint64{}

	// Set delegation shares
	delegation.Shares = math.LegacyNewDecFromInt(power)

	if err = k.SetComputeValidator(ctx, validator); err != nil {
		return math.LegacyDec{}, err
	}

	err = k.SetComputeValidatorByConsAddr(ctx, validator)
	if err != nil {
		return math.LegacyDec{}, err
	}

	if err = k.SetComputeDelegation(ctx, delegation); err != nil {
		return newShares, err
	}

	// Call the after-modification hook
	if err := k.Hooks().AfterDelegationModified(ctx, delAddr, valbz); err != nil {
		return newShares, err
	}

	return newShares, nil
}

// ClearAllComputeQueues clears all unbonding and redelegation queues since there's no unbonding in Proof of Compute
func (k Keeper) ClearAllComputeQueues(ctx context.Context) error {
	store := k.storeService.OpenKVStore(ctx)

	// Clear unbonding delegation queues
	ubdIterator, err := store.Iterator(types.UnbondingQueueKey, storetypes.PrefixEndBytes(types.UnbondingQueueKey))
	if err != nil {
		return err
	}
	defer ubdIterator.Close()

	for ; ubdIterator.Valid(); ubdIterator.Next() {
		if err := store.Delete(ubdIterator.Key()); err != nil {
			return err
		}
	}

	// Clear redelegation queues
	redIterator, err := store.Iterator(types.RedelegationQueueKey, storetypes.PrefixEndBytes(types.RedelegationQueueKey))
	if err != nil {
		return err
	}
	defer redIterator.Close()

	for ; redIterator.Valid(); redIterator.Next() {
		if err := store.Delete(redIterator.Key()); err != nil {
			return err
		}
	}

	// Clear validator unbonding queues
	valIterator, err := store.Iterator(types.ValidatorQueueKey, storetypes.PrefixEndBytes(types.ValidatorQueueKey))
	if err != nil {
		return err
	}
	defer valIterator.Close()

	for ; valIterator.Valid(); valIterator.Next() {
		if err := store.Delete(valIterator.Key()); err != nil {
			return err
		}
	}

	return nil
}
