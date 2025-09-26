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

// SetComputeValidators - Simple and clean implementation
func (k Keeper) SetComputeValidators(ctx context.Context, computeResults []ComputeResult) ([]types.Validator, error) {
	logger := k.Logger(ctx)

	// 1. Filter invalid entries and deduplicate
	validResults := k.filterValidComputeResults(ctx, computeResults)
	if len(validResults) == 0 {
		logger.Warn("No valid compute results after filtering")
		return k.GetAllValidators(ctx)
	}

	// Build map for easy lookup
	resultsMap := make(map[string]ComputeResult)
	for _, result := range validResults {
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
		if err != nil || conPubKey == nil {
			logger.Warn("Invalid validator consensus pubkey, will remove", "operator", validator.GetOperator())
			continue
		}
		validatorsAlreadyExisting[conPubKey.String()] = true
	}

	// 2. Create new validators (ones not created yet)
	for _, computeResult := range validResults {
		if computeResult.Power == 0 {
			continue // Skip zero power for new validators
		}
		pubKeyStr := computeResult.ValidatorPubKey.String()
		if !validatorsAlreadyExisting[pubKeyStr] {
			logger.Info("Creating validator", "power", computeResult.Power, "operator", computeResult.OperatorAddress)
			_, err := k.createValidatorFromComputeResult(ctx, computeResult)
			if err != nil {
				logger.Error("Error creating validator, skipping", "operator", computeResult.OperatorAddress, "error", err.Error())
				continue
			}
		}
	}

	// 3. Update all validators (including removal of zero weight ones)
	currentValidators, err = k.GetAllValidators(ctx)
	if err != nil {
		logger.Error("error refreshing validators", "error", err.Error())
		return nil, err
	}

	// Separate validators into those to update and those to remove
	var validatorsToUpdate []types.Validator
	var validatorsToRemove []types.Validator

	for _, validator := range currentValidators {
		conPubKey, err := validator.ConsPubKey()
		if err != nil || conPubKey == nil {
			logger.Warn("Invalid validator consensus pubkey, will remove", "operator", validator.GetOperator())
			validatorsToRemove = append(validatorsToRemove, validator)
			continue
		}

		pubKeyStr := conPubKey.String()
		computeResult, found := resultsMap[pubKeyStr]

		if found {
			// Validator is in compute results, update its power
			logger.Info("Updating validator", "operator", validator.GetOperator(), "power", computeResult.Power)
			validatorsToUpdate = append(validatorsToUpdate, validator)
		} else {
			// Validator is not in compute results, remove it
			logger.Info("Removing validator", "operator", validator.GetOperator())
			validatorsToRemove = append(validatorsToRemove, validator)
		}
	}

	for _, validator := range validatorsToUpdate {
		conPubKey, _ := validator.ConsPubKey()
		pubKeyStr := conPubKey.String()
		computeResult := resultsMap[pubKeyStr]

		err = k.updateValidatorPower(ctx, validator, computeResult.Power)
		if err != nil {
			logger.Error("Error updating validator, skipping", "operator", validator.GetOperator(), "error", err.Error())
			continue
		}
	}

	for _, validator := range validatorsToRemove {
		err = k.updateValidatorPower(ctx, validator, 0)
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

	// Call the before-creation hook for the delegation
	if err := k.Hooks().BeforeDelegationCreated(ctx, delegatorAccountAddress, valAddr); err != nil {
		k.Logger(ctx).Error("Error in before delegation created hook", "error", err.Error())
		return nil, err
	}

	err = k.SetComputeDelegation(ctx, delegation)
	if err != nil {
		k.Logger(ctx).Error("Error setting delegation", "error", err.Error())
		return nil, err
	}

	// Call the after-modification hook for the delegation
	if err := k.Hooks().AfterDelegationModified(ctx, delegatorAccountAddress, valAddr); err != nil {
		k.Logger(ctx).Error("Error in after delegation modified hook", "error", err.Error())
		return nil, err
	}

	return &bondedVal, nil
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
	// In some situations, the exchange rate becomes invalid, e.g. if
	// Validator loses all tokens due to slashing. In this case,
	// make all future delegations invalid.
	if validator.InvalidExRate() {
		return math.LegacyZeroDec(), types.ErrDelegatorShareExRateInvalid
	}

	// Always remove any existing power-index entries for this validator first
	if err := k.DeleteComputeValidatorByPowerIndex(ctx, validator); err != nil {
		return math.LegacyZeroDec(), err
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

	// Set power index for non-zero power validators
	if validator.Tokens.IsPositive() {
		if err = k.SetComputeValidatorByPowerIndex(ctx, validator); err != nil {
			return math.LegacyDec{}, err
		}
	}

	if err = k.SetComputeDelegation(ctx, delegation); err != nil {
		return newShares, err
	}

	// Call the after-modification hook for the delegation
	if err := k.Hooks().AfterDelegationModified(ctx, delAddr, valbz); err != nil {
		k.Logger(ctx).Error("Error in after delegation modified hook", "error", err.Error())
		return math.LegacyZeroDec(), err
	}

	// Call the after-bonded hook to ensure signing info exists
	consAddr, err := validator.GetConsAddr()
	if err != nil {
		return math.LegacyDec{}, err
	}
	if err := k.Hooks().AfterValidatorBonded(ctx, consAddr, valbz); err != nil {
		k.Logger(ctx).Error("Error in after validator bonded hook", "error", err.Error())
		return math.LegacyDec{}, err
	}

	return delegation.Shares, nil
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

// filterValidComputeResults filters and deduplicates compute results
func (k Keeper) filterValidComputeResults(ctx context.Context, computeResults []ComputeResult) []ComputeResult {
	logger := k.Logger(ctx)

	if len(computeResults) == 0 {
		return nil
	}

	// Prevent DoS attacks
	maxValidators := 1000
	if len(computeResults) > maxValidators {
		logger.Warn("Too many validators in compute results, truncating", "count", len(computeResults), "max", maxValidators)
		computeResults = computeResults[:maxValidators]
	}

	var validResults []ComputeResult
	seen := make(map[string]bool)

	for i, result := range computeResults {
		// Basic validation
		if result.ValidatorPubKey == nil {
			logger.Warn("Nil ValidatorPubKey, skipping", "index", i)
			continue
		}
		if result.OperatorAddress == "" {
			logger.Warn("Empty OperatorAddress, skipping", "index", i)
			continue
		}
		if result.Power < 0 {
			logger.Warn("Negative power, skipping", "index", i, "power", result.Power)
			continue
		}

		// Prevent overflow - max power should be reasonable
		const maxPower = 1e15 // 1 quadrillion - reasonable upper bound
		if result.Power > maxPower {
			logger.Warn("Power too large, skipping", "index", i, "power", result.Power, "max", maxPower)
			continue
		}

		// Validate pubkey and address format
		if err := safeValidatePublicKey(result.ValidatorPubKey); err != nil {
			logger.Warn("Invalid ValidatorPubKey, skipping", "index", i, "error", err.Error())
			continue
		}

		if _, err := sdk.ValAddressFromBech32(result.OperatorAddress); err != nil {
			logger.Warn("Invalid OperatorAddress format, skipping", "index", i, "address", result.OperatorAddress)
			continue
		}

		// Deduplicate by pubkey
		pubKeyStr := result.ValidatorPubKey.String()
		if seen[pubKeyStr] {
			logger.Warn("Duplicate pubkey, skipping", "index", i, "pubkey", pubKeyStr)
			continue
		}
		seen[pubKeyStr] = true

		validResults = append(validResults, result)
	}

	logger.Info("Filtered compute results", "original", len(computeResults), "valid", len(validResults))
	return validResults
}

// removeValidatorSafely removes a validator without causing panics
func (k Keeper) removeValidatorSafely(ctx context.Context, validator types.Validator) {
	logger := k.Logger(ctx)

	// Use zero power to trigger removal
	err := k.updateValidatorPower(ctx, validator, 0)
	if err != nil {
		logger.Error("Failed to remove validator safely", "operator", validator.GetOperator(), "error", err.Error())
	}
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
