package keeper

import (
	"context"
	"fmt"
	"time"

	cmtprotocrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	gogotypes "github.com/cosmos/gogoproto/types"

	corestore "cosmossdk.io/core/store"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

// GetValidator gets a single validator
func (k Keeper) GetValidator(ctx context.Context, addr sdk.ValAddress) (validator types.Validator, err error) {
	store := k.storeService.OpenKVStore(ctx)
	value, err := store.Get(types.GetValidatorKey(addr))
	if err != nil {
		return validator, err
	}

	if value == nil {
		return validator, types.ErrNoValidatorFound
	}

	return types.UnmarshalValidator(k.cdc, value)
}

func (k Keeper) mustGetValidator(ctx context.Context, addr sdk.ValAddress) types.Validator {
	validator, err := k.GetValidator(ctx, addr)
	if err != nil {
		panic(fmt.Sprintf("validator record not found for address: %X\n", addr))
	}

	return validator
}

// GetValidatorByConsAddr gets a single validator by consensus address
func (k Keeper) GetValidatorByConsAddr(ctx context.Context, consAddr sdk.ConsAddress) (validator types.Validator, err error) {
	store := k.storeService.OpenKVStore(ctx)
	opAddr, err := store.Get(types.GetValidatorByConsAddrKey(consAddr))
	if err != nil {
		return validator, err
	}

	if opAddr == nil {
		return validator, types.ErrNoValidatorFound
	}

	return k.GetValidator(ctx, opAddr)
}

func (k Keeper) mustGetValidatorByConsAddr(ctx context.Context, consAddr sdk.ConsAddress) types.Validator {
	validator, err := k.GetValidatorByConsAddr(ctx, consAddr)
	if err != nil {
		panic(fmt.Errorf("validator with consensus-Address %s not found", consAddr))
	}

	return validator
}

// SetValidator sets the main record holding validator details
func (k Keeper) SetComputeValidator(ctx context.Context, validator types.Validator) error {
	store := k.storeService.OpenKVStore(ctx)
	bz := types.MustMarshalValidator(k.cdc, &validator)
	str, err := k.ValidatorAddressCodec().StringToBytes(validator.GetOperator())
	if err != nil {
		return err
	}
	return store.Set(types.GetValidatorKey(str), bz)
}

// SetValidator sets the main record holding validator details
func (k Keeper) SetValidator(ctx context.Context, validator types.Validator) error {
	return nil
}

// SetValidatorByConsAddr sets a validator by conesensus address
func (k Keeper) SetValidatorByConsAddr(ctx context.Context, validator types.Validator) error {
	return nil
}

// SetValidatorByConsAddr sets a validator by conesensus address
func (k Keeper) SetComputeValidatorByConsAddr(ctx context.Context, validator types.Validator) error {
	consPk, err := validator.GetConsAddr()
	if err != nil {
		return err
	}
	store := k.storeService.OpenKVStore(ctx)

	bz, err := k.validatorAddressCodec.StringToBytes(validator.GetOperator())
	if err != nil {
		return err
	}

	return store.Set(types.GetValidatorByConsAddrKey(consPk), bz)
}

// SetValidatorByPowerIndex sets a validator by power index
func (k Keeper) SetComputeValidatorByPowerIndex(ctx context.Context, validator types.Validator) error {
	// jailed validators are not kept in the power index
	if validator.Jailed {
		return nil
	}

	// First, delete any existing power index entries for this validator to prevent duplicates
	if err := k.DeleteComputeValidatorByPowerIndex(ctx, validator); err != nil {
		// Log but don't fail - the entry might not exist
		k.Logger(ctx).Debug("Could not delete existing power index entry", "validator", validator.GetOperator(), "error", err.Error())
	}

	store := k.storeService.OpenKVStore(ctx)
	str, err := k.validatorAddressCodec.StringToBytes(validator.GetOperator())
	if err != nil {
		return err
	}
	return store.Set(types.GetValidatorsByPowerIndexKey(validator, k.PowerReduction(ctx), k.validatorAddressCodec), str)
}

// SetValidatorByPowerIndex sets a validator by power index
func (k Keeper) SetValidatorByPowerIndex(ctx context.Context, validator types.Validator) error {
	return nil
}

// DeleteValidatorByPowerIndex deletes a record by power index
func (k Keeper) DeleteComputeValidatorByPowerIndex(ctx context.Context, validator types.Validator) error {
	store := k.storeService.OpenKVStore(ctx)
	return store.Delete(types.GetValidatorsByPowerIndexKey(validator, k.PowerReduction(ctx), k.validatorAddressCodec))
}

// DeleteValidatorByPowerIndex deletes a record by power index
func (k Keeper) DeleteValidatorByPowerIndex(ctx context.Context, validator types.Validator) error {
	return nil
}

// SetNewValidatorByPowerIndex adds new entry by power index
func (k Keeper) SetNewValidatorByPowerIndex(ctx context.Context, validator types.Validator) error {
	return nil
}

// SetNewValidatorByPowerIndex adds new entry by power index
func (k Keeper) SetNewComputeValidatorByPowerIndex(ctx context.Context, validator types.Validator) error {
	// First, delete any existing power index entries for this validator to prevent duplicates
	if err := k.DeleteComputeValidatorByPowerIndex(ctx, validator); err != nil {
		// Log but don't fail - the entry might not exist
		k.Logger(ctx).Debug("Could not delete existing power index entry", "validator", validator.GetOperator(), "error", err.Error())
	}

	store := k.storeService.OpenKVStore(ctx)
	str, err := k.validatorAddressCodec.StringToBytes(validator.GetOperator())
	if err != nil {
		return err
	}
	return store.Set(types.GetValidatorsByPowerIndexKey(validator, k.PowerReduction(ctx), k.validatorAddressCodec), str)
}

// AddValidatorTokensAndShares updates the tokens of an existing validator, updates the validators power index key
func (k Keeper) AddValidatorTokensAndShares(ctx context.Context, validator types.Validator,
	tokensToAdd math.Int,
) (valOut types.Validator, addedShares math.LegacyDec, err error) {
	return validator, math.LegacyZeroDec(), nil
}

// RemoveValidatorTokensAndShares updates the tokens of an existing validator, updates the validators power index key
func (k Keeper) RemoveValidatorTokensAndShares(ctx context.Context, validator types.Validator,
	sharesToRemove math.LegacyDec,
) (valOut types.Validator, removedTokens math.Int, err error) {
	return validator, math.ZeroInt(), nil
}

// RemoveValidatorTokens updates the tokens of an existing validator, updates the validators power index key
func (k Keeper) RemoveValidatorTokens(ctx context.Context,
	validator types.Validator, tokensToRemove math.Int,
) (types.Validator, error) {
	return validator, nil
}

// UpdateValidatorCommission attempts to update a validator's commission rate.
// An error is returned if the new commission rate is invalid.
func (k Keeper) UpdateValidatorCommission(ctx context.Context,
	validator types.Validator, newRate math.LegacyDec,
) (types.Commission, error) {
	commission := validator.Commission
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	blockTime := sdkCtx.BlockHeader().Time

	if err := commission.ValidateNewRate(newRate, blockTime); err != nil {
		return commission, err
	}

	minCommissionRate, err := k.MinCommissionRate(ctx)
	if err != nil {
		return commission, err
	}

	if newRate.LT(minCommissionRate) {
		return commission, fmt.Errorf("cannot set validator commission to less than minimum rate of %s", minCommissionRate)
	}

	commission.Rate = newRate
	commission.UpdateTime = blockTime

	return commission, nil
}

// RemoveComputeValidator removes the validator record and associated indexes for Proof of Compute
func (k Keeper) RemoveComputeValidator(ctx context.Context, address sdk.ValAddress) error {
	// first retrieve the old validator record
	validator, err := k.GetValidator(ctx, address)
	if err != nil {
		// If validator doesn't exist, that's fine - already removed
		return nil
	}

	valConsAddr, err := validator.GetConsAddr()
	if err != nil {
		return err
	}

	// delete the validator record and indexes
	store := k.storeService.OpenKVStore(ctx)
	if err = store.Delete(types.GetValidatorKey(address)); err != nil {
		return err
	}

	if err = store.Delete(types.GetValidatorByConsAddrKey(valConsAddr)); err != nil {
		return err
	}

	if err = store.Delete(types.GetValidatorsByPowerIndexKey(validator, k.PowerReduction(ctx), k.validatorAddressCodec)); err != nil {
		return err
	}

	str, err := k.validatorAddressCodec.StringToBytes(validator.GetOperator())
	if err != nil {
		return err
	}

	if err := k.Hooks().AfterValidatorRemoved(ctx, valConsAddr, str); err != nil {
		k.Logger(ctx).Error("error in after validator removed hook", "error", err)
	}

	return nil
}

// RemoveValidator removes the validator record and associated indexes
// except for the bonded validator index which is only handled in ApplyAndReturnTendermintUpdates
func (k Keeper) RemoveValidator(ctx context.Context, address sdk.ValAddress) error {
	return nil
}

// get groups of validators

// GetAllValidators gets the set of all validators with no limits, used during genesis dump
func (k Keeper) GetAllValidators(ctx context.Context) (validators []types.Validator, err error) {
	store := k.storeService.OpenKVStore(ctx)

	iterator, err := store.Iterator(types.ValidatorsKey, storetypes.PrefixEndBytes(types.ValidatorsKey))
	if err != nil {
		return nil, err
	}
	defer iterator.Close()

	for ; iterator.Valid(); iterator.Next() {
		validator, err := types.UnmarshalValidator(k.cdc, iterator.Value())
		if err != nil {
			return nil, err
		}
		validators = append(validators, validator)
	}

	return validators, nil
}

// GetValidators returns a given amount of all the validators
func (k Keeper) GetValidators(ctx context.Context, maxRetrieve uint32) (validators []types.Validator, err error) {
	store := k.storeService.OpenKVStore(ctx)
	validators = make([]types.Validator, maxRetrieve)

	iterator, err := store.Iterator(types.ValidatorsKey, storetypes.PrefixEndBytes(types.ValidatorsKey))
	if err != nil {
		return nil, err
	}

	i := 0
	for ; iterator.Valid() && i < int(maxRetrieve); iterator.Next() {
		validator, err := types.UnmarshalValidator(k.cdc, iterator.Value())
		if err != nil {
			return nil, err
		}
		validators[i] = validator
		i++
	}

	return validators[:i], nil // trim if the array length < maxRetrieve
}

// GetBondedValidatorsByPower gets the current group of bonded validators sorted by power-rank
func (k Keeper) GetBondedValidatorsByPower(ctx context.Context) ([]types.Validator, error) {
	maxValidators, err := k.MaxValidators(ctx)
	if err != nil {
		return nil, err
	}
	validators := make([]types.Validator, maxValidators)

	iterator, err := k.ValidatorsPowerStoreIterator(ctx)
	if err != nil {
		return nil, err
	}
	defer iterator.Close()

	i := 0
	for ; iterator.Valid() && i < int(maxValidators); iterator.Next() {
		address := iterator.Value()
		validator := k.mustGetValidator(ctx, address)

		if validator.IsBonded() {
			validators[i] = validator
			i++
		}
	}

	return validators[:i], nil // trim
}

// ValidatorsPowerStoreIterator returns an iterator for the current validator power store
func (k Keeper) ValidatorsPowerStoreIterator(ctx context.Context) (corestore.Iterator, error) {
	store := k.storeService.OpenKVStore(ctx)
	return store.ReverseIterator(types.ValidatorsByPowerIndexKey, storetypes.PrefixEndBytes(types.ValidatorsByPowerIndexKey))
}

// Last Validator Index

// GetLastValidatorPower loads the last validator power.
// Returns zero if the operator was not a validator last block.
func (k Keeper) GetLastValidatorPower(ctx context.Context, operator sdk.ValAddress) (power int64, err error) {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(types.GetLastValidatorPowerKey(operator))
	if err != nil {
		return 0, err
	}

	if bz == nil {
		return 0, nil
	}

	intV := gogotypes.Int64Value{}
	err = k.cdc.Unmarshal(bz, &intV)
	if err != nil {
		return 0, err
	}

	return intV.GetValue(), nil
}

// SetLastValidatorPower sets the last validator power.
func (k Keeper) SetLastValidatorPower(ctx context.Context, operator sdk.ValAddress, power int64) error {
	return nil
}

// SetComputeLastValidatorPower sets the last validator power for Proof of Compute.
func (k Keeper) SetComputeLastValidatorPower(ctx context.Context, operator sdk.ValAddress, power int64) error {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := k.cdc.Marshal(&gogotypes.Int64Value{Value: power})
	if err != nil {
		return err
	}
	return store.Set(types.GetLastValidatorPowerKey(operator), bz)
}

// DeleteLastValidatorPower deletes the last validator power.
func (k Keeper) DeleteLastValidatorPower(ctx context.Context, operator sdk.ValAddress) error {
	return nil
}

// DeleteComputeLastValidatorPower deletes the last validator power for Proof of Compute.
func (k Keeper) DeleteComputeLastValidatorPower(ctx context.Context, operator sdk.ValAddress) error {
	store := k.storeService.OpenKVStore(ctx)
	return store.Delete(types.GetLastValidatorPowerKey(operator))
}

// lastValidatorsIterator returns an iterator for the consensus validators in the last block
func (k Keeper) LastValidatorsIterator(ctx context.Context) (corestore.Iterator, error) {
	store := k.storeService.OpenKVStore(ctx)
	return store.Iterator(types.LastValidatorPowerKey, storetypes.PrefixEndBytes(types.LastValidatorPowerKey))
}

// IterateLastValidatorPowers iterates over last validator powers.
func (k Keeper) IterateLastValidatorPowers(ctx context.Context, handler func(operator sdk.ValAddress, power int64) (stop bool)) error {
	iter, err := k.LastValidatorsIterator(ctx)
	if err != nil {
		return err
	}

	for ; iter.Valid(); iter.Next() {
		addr := sdk.ValAddress(types.AddressFromLastValidatorPowerKey(iter.Key()))
		intV := &gogotypes.Int64Value{}

		if err = k.cdc.Unmarshal(iter.Value(), intV); err != nil {
			return err
		}

		if handler(addr, intV.GetValue()) {
			break
		}
	}

	return nil
}

// GetLastValidators gets the group of the bonded validators
func (k Keeper) GetLastValidators(ctx context.Context) (validators []types.Validator, err error) {
	store := k.storeService.OpenKVStore(ctx)

	// add the actual validator power sorted store
	maxValidators, err := k.MaxValidators(ctx)
	if err != nil {
		return nil, err
	}
	validators = make([]types.Validator, maxValidators)

	iterator, err := store.Iterator(types.LastValidatorPowerKey, storetypes.PrefixEndBytes(types.LastValidatorPowerKey))
	if err != nil {
		return nil, err
	}
	defer iterator.Close()

	i := 0
	for ; iterator.Valid(); iterator.Next() {
		// sanity check
		if i >= int(maxValidators) {
			panic("more validators than maxValidators found")
		}

		address := types.AddressFromLastValidatorPowerKey(iterator.Key())
		validator, err := k.GetValidator(ctx, address)
		if err != nil {
			return nil, err
		}

		validators[i] = validator
		i++
	}

	return validators[:i], nil // trim
}

// GetUnbondingValidators returns a slice of mature validator addresses that
// complete their unbonding at a given time and height.
func (k Keeper) GetUnbondingValidators(ctx context.Context, endTime time.Time, endHeight int64) ([]string, error) {
	store := k.storeService.OpenKVStore(ctx)

	bz, err := store.Get(types.GetValidatorQueueKey(endTime, endHeight))
	if err != nil {
		return nil, err
	}

	if bz == nil {
		return []string{}, nil
	}

	addrs := types.ValAddresses{}
	if err = k.cdc.Unmarshal(bz, &addrs); err != nil {
		return nil, err
	}

	return addrs.Addresses, nil
}

// SetUnbondingValidatorsQueue sets a given slice of validator addresses into
// the unbonding validator queue by a given height and time.
func (k Keeper) SetUnbondingValidatorsQueue(ctx context.Context, endTime time.Time, endHeight int64, addrs []string) error {
	return nil
}

// InsertUnbondingValidatorQueue inserts a given unbonding validator address into
// the unbonding validator queue for a given height and time.
func (k Keeper) InsertUnbondingValidatorQueue(ctx context.Context, val types.Validator) error {
	return nil
}

// DeleteValidatorQueueTimeSlice deletes all entries in the queue indexed by a
// given height and time.
func (k Keeper) DeleteValidatorQueueTimeSlice(ctx context.Context, endTime time.Time, endHeight int64) error {
	return nil
}

// DeleteValidatorQueue removes a validator by address from the unbonding queue
// indexed by a given height and time.
func (k Keeper) DeleteValidatorQueue(ctx context.Context, val types.Validator) error {
	return nil
}

// ValidatorQueueIterator returns an interator ranging over validators that are
// unbonding whose unbonding completion occurs at the given height and time.
func (k Keeper) ValidatorQueueIterator(ctx context.Context, endTime time.Time, endHeight int64) (corestore.Iterator, error) {
	store := k.storeService.OpenKVStore(ctx)
	return store.Iterator(types.ValidatorQueueKey, storetypes.InclusiveEndBytes(types.GetValidatorQueueKey(endTime, endHeight)))
}

// UnbondAllMatureValidators unbonds all the mature unbonding validators that
// have finished their unbonding period.
func (k Keeper) UnbondAllMatureValidators(ctx context.Context) error {
	return nil
}

// IsValidatorJailed checks and returns boolean of a validator status jailed or not.
func (k Keeper) IsValidatorJailed(ctx context.Context, addr sdk.ConsAddress) (bool, error) {
	v, err := k.GetValidatorByConsAddr(ctx, addr)
	if err != nil {
		return false, err
	}

	return v.Jailed, nil
}

// GetPubKeyByConsAddr returns the consensus public key by consensus address.
func (k Keeper) GetPubKeyByConsAddr(ctx context.Context, addr sdk.ConsAddress) (cmtprotocrypto.PublicKey, error) {
	v, err := k.GetValidatorByConsAddr(ctx, addr)
	if err != nil {
		return cmtprotocrypto.PublicKey{}, err
	}

	pubkey, err := v.CmtConsPublicKey()
	if err != nil {
		return cmtprotocrypto.PublicKey{}, err
	}

	return pubkey, nil
}
