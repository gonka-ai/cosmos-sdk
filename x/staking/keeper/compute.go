package keeper

import (
	"context"
	"fmt"
	"sort"

	"cosmossdk.io/log"
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

	// Basic filter first: any downstream logic assumes non-nil pubkeys and positive power.
	beforeBasicFilter := computeResults
	computeResults = filterInvalidComputeResults(computeResults)

	// Stage-by-stage logging so we can understand how computeResults evolves through filters.
	// This is intentionally lightweight (O(n) per stage) and logs only aggregated counts.
	if removed := len(beforeBasicFilter) - len(computeResults); removed > 0 {
		logger.Info("compute results filtered invalid entries", "removed", removed, "before_total", len(beforeBasicFilter), "after_total", len(computeResults))
	}

	initialStats := computeResultsStatsFrom(computeResults)
	logger.Info(
		"compute results stats",
		"stage", "initial",
		"total", initialStats.Total,
		"unique_operator_addrs", initialStats.UniqueOperatorAddrs,
		"unique_consensus_keys", initialStats.UniqueConsensusKeys,
		"dup_operator_entries", initialStats.DuplicateOperatorEntries,
		"dup_consensus_entries", initialStats.DuplicateConsensusEntries,
	)

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

	sortComputeResultsInplace(computeResults)
	afterSortStats := computeResultsStatsFrom(computeResults)
	logger.Info(
		"compute results stats",
		"stage", "after_sort",
		"total", afterSortStats.Total,
		"unique_operator_addrs", afterSortStats.UniqueOperatorAddrs,
		"unique_consensus_keys", afterSortStats.UniqueConsensusKeys,
		"dup_operator_entries", afterSortStats.DuplicateOperatorEntries,
		"dup_consensus_entries", afterSortStats.DuplicateConsensusEntries,
	)

	beforeFilter := computeResults
	computeResults = filterBasedOnExisting(ctx, computeResults, currentValsByConsensusAddress, currentValsByOperatorAddress)
	logComputeResultsFilterStats(logger, "filter_based_on_existing", beforeFilter, computeResults)

	beforeFilter = computeResults
	computeResults = filterDuplicateOperatorAddresses(ctx, computeResults)
	logComputeResultsFilterStats(logger, "filter_duplicate_operator_addresses", beforeFilter, computeResults)

	beforeFilter = computeResults
	computeResults = filterDuplicateConsensusKeys(ctx, computeResults)
	logComputeResultsFilterStats(logger, "filter_duplicate_consensus_keys", beforeFilter, computeResults)

	resultsByOperatorAddress := make(map[string]ComputeResult)
	for _, res := range computeResults {
		resultsByOperatorAddress[res.OperatorAddress] = res
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

func sortComputeResultsInplace(computeResults []ComputeResult) {
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
		if iPubKey == jPubKey {
			return computeResults[i].Power > computeResults[j].Power
		}
		return iPubKey < jPubKey
	})
}

func filterInvalidComputeResults(computeResults []ComputeResult) []ComputeResult {
	filtered := make([]ComputeResult, 0, len(computeResults))
	for _, res := range computeResults {
		if res.OperatorAddress == "" || res.ValidatorPubKey == nil || res.Power <= 0 {
			continue
		}
		filtered = append(filtered, res)
	}
	return filtered
}

func filterBasedOnExisting(
	ctx context.Context,
	computeResults []ComputeResult,
	currentValsByConsensusAddress map[string]types.Validator,
	currentValsByOperatorAddress map[string]types.Validator,
) []ComputeResult {
	logger := sdk.UnwrapSDKContext(ctx).Logger()

	filtered := make([]ComputeResult, 0, len(computeResults))
	for _, res := range computeResults {
		if res.ValidatorPubKey == nil || res.Power <= 0 {
			continue
		}

		consensusAddress := res.ValidatorPubKey.Address().String()
		if val, exists := currentValsByConsensusAddress[consensusAddress]; exists {
			if val.OperatorAddress != res.OperatorAddress {
				logger.Warn("validator with the same consensus pubkey already exists, rejecting a new one", "consensusAddress", consensusAddress, "existingValidator", val.OperatorAddress, "newValidator", res.OperatorAddress)
				continue
			}
		}

		val, exists := currentValsByOperatorAddress[res.OperatorAddress]
		if exists && val.ConsensusPubkey.GetCachedValue().(cryptotypes.PubKey).Address().String() != res.ValidatorPubKey.Address().String() {
			logger.Warn("validator changed consensus pubkey, removing from validator set", "operator", val.OperatorAddress, "existingConsensusKey", val.ConsensusPubkey.GetCachedValue().(cryptotypes.PubKey).Address().String(), "newConsensusKey", res.ValidatorPubKey.Address().String())
			continue
		}

		filtered = append(filtered, res)
	}

	return filtered
}

func filterDuplicateOperatorAddresses(ctx context.Context, computeResults []ComputeResult) []ComputeResult {
	logger := sdk.UnwrapSDKContext(ctx).Logger()
	filtered := make([]ComputeResult, 0, len(computeResults))
	seen := make(map[string]bool)
	for _, res := range computeResults {
		if _, exists := seen[res.OperatorAddress]; exists {
			logger.Warn("duplicate operator address found in compute results, skipping", "operatorAddress", res.OperatorAddress)
			continue
		}

		seen[res.OperatorAddress] = true
		filtered = append(filtered, res)
	}
	return filtered
}

func filterDuplicateConsensusKeys(ctx context.Context, computeResults []ComputeResult) []ComputeResult {
	logger := sdk.UnwrapSDKContext(ctx).Logger()
	filtered := make([]ComputeResult, 0, len(computeResults))
	seen := make(map[string]bool)
	for _, res := range computeResults {
		consensusAddress := res.ValidatorPubKey.Address().String()
		if _, exists := seen[consensusAddress]; exists {
			logger.Warn("duplicate consensus key found in compute results, skipping", "consensusAddress", consensusAddress, "operatorAddress", res.OperatorAddress)
			continue
		}

		seen[consensusAddress] = true
		filtered = append(filtered, res)
	}
	return filtered
}

type computeResultsStats struct {
	Total                     int
	UniqueOperatorAddrs       int
	UniqueConsensusKeys       int
	DuplicateOperatorEntries  int
	DuplicateConsensusEntries int
}

func computeResultsStatsFrom(computeResults []ComputeResult) computeResultsStats {
	stats := computeResultsStats{Total: len(computeResults)}

	seenOperator := make(map[string]int, len(computeResults))
	seenConsensus := make(map[string]int, len(computeResults))

	for _, res := range computeResults {
		if res.ValidatorPubKey != nil {
			consAddr := res.ValidatorPubKey.Address().String()
			seenConsensus[consAddr]++
		}

		seenOperator[res.OperatorAddress]++
	}

	stats.UniqueOperatorAddrs = len(seenOperator)
	stats.UniqueConsensusKeys = len(seenConsensus)

	for _, c := range seenOperator {
		if c > 1 {
			stats.DuplicateOperatorEntries += (c - 1)
		}
	}
	for _, c := range seenConsensus {
		if c > 1 {
			stats.DuplicateConsensusEntries += (c - 1)
		}
	}

	return stats
}

func logComputeResultsFilterStats(logger log.Logger, stage string, before, after []ComputeResult) {
	beforeStats := computeResultsStatsFrom(before)
	afterStats := computeResultsStatsFrom(after)

	removed := beforeStats.Total - afterStats.Total
	logger.Info(
		"compute results stats",
		"stage", stage,
		"removed", removed,
		"before_total", beforeStats.Total,
		"after_total", afterStats.Total,
		"before_unique_operator_addrs", beforeStats.UniqueOperatorAddrs,
		"after_unique_operator_addrs", afterStats.UniqueOperatorAddrs,
		"before_unique_consensus_keys", beforeStats.UniqueConsensusKeys,
		"after_unique_consensus_keys", afterStats.UniqueConsensusKeys,
		"before_dup_operator_entries", beforeStats.DuplicateOperatorEntries,
		"after_dup_operator_entries", afterStats.DuplicateOperatorEntries,
		"before_dup_consensus_entries", beforeStats.DuplicateConsensusEntries,
		"after_dup_consensus_entries", afterStats.DuplicateConsensusEntries,
	)
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
