package keeper

import (
	"context"
	"fmt"
	"sort"

	"cosmossdk.io/math"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

// ComputeResult defines the structure for validator power updates.
type ComputeResult struct {
	Power           int64
	ValidatorPubKey cryptotypes.PubKey
	OperatorAddress string
}

const ValidatorIndexFixHeight = 658087

// SetComputeValidators before validator index fix height
func (k Keeper) SetComputeValidatorsBeforeValidatorIndexFixHeight(ctx context.Context, computeResults []ComputeResult) ([]types.Validator, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	logger := k.Logger(sdkCtx)

	resultsMap := make(map[string]ComputeResult)
	for _, res := range computeResults {
		if res.ValidatorPubKey == nil {
			continue
		}
		resultsMap[res.OperatorAddress] = res
	}

	currentValidators, err := k.GetAllValidators(ctx)
	if err != nil {
		logger.Error("failed to get all validators", "error", err)
		return nil, err
	}

	currentValMap := make(map[string]types.Validator)
	for _, val := range currentValidators {
		currentValMap[val.OperatorAddress] = val
	}

	for pubKeyAddr, result := range resultsMap {
		val, found := currentValMap[pubKeyAddr]

		power := math.NewInt(result.Power)
		if power.IsNegative() {
			logger.Info("skipping validator with negative power", "pubkey", result.ValidatorPubKey.Address())
			continue
		}

		if !found {
			if power.IsZero() {
				continue
			}
			logger.Info("creating new validator", "pubkey", result.ValidatorPubKey.Address(), "power", power)
			if err := k.createValidatorImmediate(ctx, result.OperatorAddress, result.ValidatorPubKey, power); err != nil {
				logger.Error("failed to create validator", "pubkey", result.ValidatorPubKey.Address(), "error", err)
			}
		} else {
			if val.Tokens == power && val.IsBonded() && !val.Jailed {
				continue
			}

			if power.IsZero() {
				// Mark for deletion - actual deletion happens in BlockValidatorUpdates
				logger.Info("marking validator for removal (zero power)", "operator", val.OperatorAddress)
				if err := k.markValidatorForDeletion(ctx, val); err != nil {
					logger.Error("failed to mark validator for deletion", "operator", val.OperatorAddress, "error", err)
				}
			} else {
				logger.Info("updating validator power", "operator", val.OperatorAddress, "new_power", power)
				if err := k.updateValidator(ctx, val, power); err != nil {
					logger.Error("failed to update validator power", "operator", val.OperatorAddress, "error", err)
				}
			}
		}
	}

	// Mark validators for deletion that are no longer in the compute results
	for consAddrStr, val := range currentValMap {
		if _, exists := resultsMap[consAddrStr]; !exists {
			logger.Info("marking validator for removal (not in compute results)", "operator", val.OperatorAddress, "status", val.Status, "jailed", val.Jailed)
			if err := k.markValidatorForDeletion(ctx, val); err != nil {
				logger.Error("failed to mark validator for deletion", "operator", val.OperatorAddress, "error", err)
			}
		}
	}

	return k.GetAllValidators(ctx)
}

// SetComputeValidators is the main entry point for updating the validator set.
// It synchronizes the state with the provided list of compute results.
func (k Keeper) SetComputeValidators(
	ctx context.Context,
	computeResults []ComputeResult,
	isTestnet bool,
) ([]types.Validator, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentHeight := sdkCtx.BlockHeight()
	if currentHeight < ValidatorIndexFixHeight && !isTestnet {
		return k.SetComputeValidatorsBeforeValidatorIndexFixHeight(ctx, computeResults)
	}
	logger := k.Logger(sdkCtx)

	sort.Slice(computeResults, func(i, j int) bool {
		if computeResults[i].OperatorAddress != computeResults[j].OperatorAddress {
			return computeResults[i].OperatorAddress < computeResults[j].OperatorAddress
		}
		iPubKey := ""
		jPubKey := ""
		if computeResults[i].ValidatorPubKey != nil {
			iPubKey = computeResults[i].ValidatorPubKey.Address().String()
		}
		if computeResults[j].ValidatorPubKey != nil {
			jPubKey = computeResults[j].ValidatorPubKey.Address().String()
		}
		return iPubKey < jPubKey
	})

	resultsByOperatorAddress := make(map[string]ComputeResult)
	consensusPubkeyToOperator := make(map[string]string) // consensusAddr -> operatorAddr

	for _, res := range computeResults {
		if res.ValidatorPubKey == nil || res.Power <= 0 {
			continue
		}

		consensusAddr := res.ValidatorPubKey.Address().String()

		// If this operator address was already processed with a different consensus key,
		// remove the old consensus key mapping to avoid "ghost" claims.
		if oldRes, exists := resultsByOperatorAddress[res.OperatorAddress]; exists {
			oldConsAddr := oldRes.ValidatorPubKey.Address().String()
			if oldConsAddr != consensusAddr {
				delete(consensusPubkeyToOperator, oldConsAddr)
			}
		}

		if existingOp, seen := consensusPubkeyToOperator[consensusAddr]; seen {
			// Keep the lexicographically latest operator address
			if res.OperatorAddress > existingOp {
				logger.Info("duplicate consensus key found, replacing validator",
					"consensus_addr", consensusAddr,
					"old_operator", existingOp,
					"new_operator", res.OperatorAddress)
				delete(resultsByOperatorAddress, existingOp)
				consensusPubkeyToOperator[consensusAddr] = res.OperatorAddress
				resultsByOperatorAddress[res.OperatorAddress] = res
			} else {
				logger.Info("duplicate consensus key found, keeping existing validator",
					"consensus_addr", consensusAddr,
					"existing_operator", existingOp,
					"ignored_operator", res.OperatorAddress)
			}
			continue
		}

		consensusPubkeyToOperator[consensusAddr] = res.OperatorAddress
		resultsByOperatorAddress[res.OperatorAddress] = res
	}

	logger.Info("Keys deduplication is finished")

	currentValidators, err := k.GetAllValidators(ctx)
	if err != nil {
		logger.Error("failed to get all validators", "error", err)
		return nil, err
	}

	currentValsByConsensusAddress := make(map[string]types.Validator)
	currentValsByOperatorAddress := make(map[string]types.Validator)
	for _, val := range currentValidators {
		currentValsByOperatorAddress[val.OperatorAddress] = val
		consensusPubKey, err := val.ConsPubKey()
		if err != nil {
			logger.Error("failed to get validator pubkey", "operator", val.OperatorAddress, "error", err)
			continue
		}
		consensusAddress := consensusPubKey.Address().String()
		currentValsByConsensusAddress[consensusAddress] = val
	}

	for _, res := range computeResults {
		if res.ValidatorPubKey == nil {
			continue
		}
		consensusAddress := res.ValidatorPubKey.Address().String()
		if val, exists := currentValsByConsensusAddress[consensusAddress]; exists {
			if val.OperatorAddress != res.OperatorAddress {
				logger.Warn("different validator with the same consensus pubkey", "operator", val.OperatorAddress, "expected", val.OperatorAddress, "got", res.OperatorAddress)
				delete(resultsByOperatorAddress, res.OperatorAddress)
			}
		}

		val, exists := currentValsByOperatorAddress[res.OperatorAddress]
		if exists && val.ConsensusPubkey.GetCachedValue().(cryptotypes.PubKey).Address().String() != res.ValidatorPubKey.Address().String() {
			logger.Warn("validator changed consensus pubkey, removing from validator set", "operator", val.OperatorAddress, "expected", val.ConsensusPubkey.GetCachedValue().(cryptotypes.PubKey).Address().String(), "got", res.ValidatorPubKey.Address().String())
			delete(resultsByOperatorAddress, res.OperatorAddress)
			continue
		}

	}

	// Sort keys for deterministic iteration
	currentValKeys := make([]string, 0, len(currentValsByOperatorAddress))
	for k := range currentValsByOperatorAddress {
		currentValKeys = append(currentValKeys, k)
	}
	sort.Strings(currentValKeys)

	// Mark validators for deletion that are no longer in the compute results
	for _, operatorAddress := range currentValKeys {
		val := currentValsByOperatorAddress[operatorAddress]
		if _, exists := resultsByOperatorAddress[operatorAddress]; !exists {
			logger.Info("marking validator for removal (not in compute results)", "operator", val.OperatorAddress, "status", val.Status, "jailed", val.Jailed)
			if err := k.markValidatorForDeletion(ctx, val); err != nil {
				logger.Error("failed to mark validator for deletion", "operator", val.OperatorAddress, "error", err)
			}
		}
	}

	// Sort keys for deterministic iteration
	resultKeys := make([]string, 0, len(resultsByOperatorAddress))
	for k := range resultsByOperatorAddress {
		resultKeys = append(resultKeys, k)
	}
	sort.Strings(resultKeys)

	for _, operatorAddress := range resultKeys {
		result := resultsByOperatorAddress[operatorAddress]
		val, found := currentValsByOperatorAddress[operatorAddress]
		power := math.NewInt(result.Power)

		if !found {
			logger.Info("creating new validator", "pubkey", result.ValidatorPubKey.Address(), "power", power)
			if err := k.createValidatorImmediate(ctx, result.OperatorAddress, result.ValidatorPubKey, power); err != nil {
				logger.Error("failed to create validator", "pubkey", result.ValidatorPubKey.Address(), "error", err)
			}
		} else {
			if val.Tokens == power && val.IsBonded() && !val.Jailed {
				continue
			}

			if !power.IsZero() {
				logger.Info("updating validator power", "operator", val.OperatorAddress, "new_power", power)
				if err := k.updateValidator(ctx, val, power); err != nil {
					logger.Error("failed to update validator power", "operator", val.OperatorAddress, "error", err)
				}
			}
		}
	}

	return k.GetAllValidators(ctx)
}

// createValidatorImmediate creates and bonds a new validator.
func (k Keeper) createValidatorImmediate(ctx context.Context, operatorAddress string, pubkey cryptotypes.PubKey, power math.Int) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	logger := k.Logger(sdkCtx)

	valAddr, err := k.ValidatorAddressCodec().StringToBytes(operatorAddress)
	if err != nil {
		logger.Error("failed to convert operator address to bytes", "address", operatorAddress, "error", err)
		return fmt.Errorf("invalid operator address %s: %w", operatorAddress, err)
	}

	// Create the validator object
	validator, err := types.NewValidator(operatorAddress, pubkey, types.Description{Moniker: operatorAddress})
	if err != nil {
		logger.Error("failed to create new validator", "operator", operatorAddress, "error", err)
		return err
	}
	validator.Status = types.Bonded // Set as bonded immediately
	validator.Tokens = power
	validator.DelegatorShares = math.LegacyNewDecFromInt(power)

	// Save validator to store
	if err := k.SetValidator(ctx, validator); err != nil {
		logger.Error("failed to set validator", "operator", operatorAddress, "error", err)
		return err
	}
	if err := k.SetValidatorByConsAddr(ctx, validator); err != nil {
		logger.Error("failed to set validator by cons addr", "operator", operatorAddress, "error", err)
		return err
	}
	if err := k.SetValidatorByPowerIndex(ctx, validator); err != nil {
		logger.Error("failed to set validator by power index", "operator", operatorAddress, "error", err)
		return err
	}

	// Create self-delegation (needed for module compatibility even if distribution is disabled)
	delegator := sdk.AccAddress(valAddr)
	delegation := types.NewDelegation(delegator.String(), operatorAddress, math.LegacyNewDecFromInt(power))
	if err := k.SetDelegation(ctx, delegation); err != nil {
		logger.Error("failed to set delegation", "delegator", delegator.String(), "validator", operatorAddress, "error", err)
		return err
	}

	if err := k.Hooks().AfterValidatorCreated(ctx, valAddr); err != nil {
		logger.Error("failed to call AfterValidatorCreated hook", "validator", operatorAddress, "error", err)
		return err
	}
	consAddr, err := validator.GetConsAddr()
	if err != nil {
		logger.Error("failed to get validator cons addr", "validator", operatorAddress, "error", err)
		return err
	}
	if err := k.Hooks().AfterValidatorBonded(ctx, consAddr, valAddr); err != nil {
		logger.Error("failed to call AfterValidatorBonded hook", "validator", operatorAddress, "error", err)
		return err
	}

	if err := k.Hooks().AfterDelegationModified(ctx, delegator, valAddr); err != nil {
		logger.Error("failed to call AfterDelegationModified hook", "delegator", delegator.String(), "validator", operatorAddress, "error", err)
		return err
	}

	return nil
}

// updateValidator updates an existing validator's power.
func (k Keeper) updateValidator(ctx context.Context, validator types.Validator, newPower math.Int) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	logger := k.Logger(sdkCtx)

	if err := k.DeleteValidatorByPowerIndex(ctx, validator); err != nil {
		logger.Error("failed to delete validator by power index", "validator", validator.OperatorAddress, "error", err)
		return err
	}

	oldStatus := validator.Status
	oldJailed := validator.Jailed

	validator.Tokens = newPower
	validator.DelegatorShares = math.LegacyNewDecFromInt(newPower)
	validator.Status = types.Bonded // Ensure validator is bonded
	validator.Jailed = false        // Unjail if it was jailed

	if err := k.SetValidator(ctx, validator); err != nil {
		logger.Error("failed to set validator for power update", "validator", validator.OperatorAddress, "error", err)
		return err
	}
	if err := k.SetValidatorByConsAddr(ctx, validator); err != nil {
		logger.Error("failed to set validator by cons addr for power update", "validator", validator.OperatorAddress, "error", err)
		return err
	}
	if err := k.SetValidatorByPowerIndex(ctx, validator); err != nil {
		logger.Error("failed to set validator by power index for power update", "validator", validator.OperatorAddress, "error", err)
		return err
	}

	valAddr, err := k.ValidatorAddressCodec().StringToBytes(validator.OperatorAddress)
	if err != nil {
		logger.Error("failed to convert operator address for delegation update", "address", validator.OperatorAddress, "error", err)
		return err
	}
	delegator := sdk.AccAddress(valAddr)
	delegation, err := k.GetDelegation(ctx, delegator, valAddr)
	if err != nil {
		delegation = types.NewDelegation(delegator.String(), validator.OperatorAddress, math.LegacyNewDecFromInt(newPower))
	} else {
		delegation.Shares = math.LegacyNewDecFromInt(newPower)
	}
	if err := k.SetDelegation(ctx, delegation); err != nil {
		logger.Error("failed to set delegation for power update", "validator", validator.OperatorAddress, "error", err)
		return err
	}

	statusChanged := oldStatus != types.Bonded && validator.Status == types.Bonded
	wasUnjailed := oldJailed && !validator.Jailed
	if statusChanged || wasUnjailed {
		consAddr, err := validator.GetConsAddr()
		if err != nil {
			logger.Error("failed to get validator cons addr for bonded hook", "validator", validator.OperatorAddress, "error", err)
			return err
		}
		if err := k.Hooks().AfterValidatorBonded(ctx, consAddr, valAddr); err != nil {
			logger.Error("failed to call AfterValidatorBonded hook for power update", "validator", validator.OperatorAddress, "error", err)
			return err
		}
	}

	if err := k.Hooks().AfterDelegationModified(ctx, delegator, valAddr); err != nil {
		logger.Error("failed to call AfterDelegationModified hook for power update", "validator", validator.OperatorAddress, "error", err)
		return err
	}

	return nil
}

// markValidatorForDeletion sets validator power to zero for immediate deletion.
// In Proof of Compute, we skip the unbonding period since there are no tokens to lock.
// Jailed validators are deleted immediately; others are processed in the next BlockValidatorUpdates.
func (k Keeper) markValidatorForDeletion(ctx context.Context, validator types.Validator) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	logger := k.Logger(sdkCtx)

	valAddr, err := k.ValidatorAddressCodec().StringToBytes(validator.OperatorAddress)
	if err != nil {
		logger.Error("failed to convert operator address", "address", validator.OperatorAddress, "error", err)
		return err
	}

	// Jailed validators can't be added to power index, so delete them immediately
	if validator.Jailed {
		logger.Info("deleting jailed validator immediately", "operator", validator.OperatorAddress)
		return k.deleteValidatorInternal(ctx, validator, valAddr)
	}

	// For non-jailed validators, mark for deletion in next block
	if err := k.DeleteValidatorByPowerIndex(ctx, validator); err != nil {
		logger.Error("failed to delete validator by power index", "validator", validator.OperatorAddress, "error", err)
		return err
	}

	// Set power to zero (status kept as-is for ApplyAndReturnValidatorSetUpdates)
	validator.Tokens = math.ZeroInt()
	validator.DelegatorShares = math.LegacyZeroDec()
	validator.UnbondingIds = []uint64{}

	if err := k.SetValidator(ctx, validator); err != nil {
		logger.Error("failed to set validator with zero power", "validator", validator.OperatorAddress, "error", err)
		return err
	}

	// Re-add to power index with zero power so ApplyAndReturnValidatorSetUpdates processes it
	if err := k.SetValidatorByPowerIndex(ctx, validator); err != nil {
		logger.Error("failed to set validator by power index with zero power", "validator", validator.OperatorAddress, "error", err)
		return err
	}

	// Zero out delegation shares
	delegator := sdk.AccAddress(valAddr)
	delegation, err := k.GetDelegation(ctx, delegator, valAddr)
	if err == nil {
		delegation.Shares = math.LegacyZeroDec()
		if err := k.SetDelegation(ctx, delegation); err != nil {
			logger.Error("failed to zero delegation shares", "validator", validator.OperatorAddress, "error", err)
			return err
		}
	}

	return nil
}
