package keeper

import (
	"context"
	"errors"

	errorsmod "cosmossdk.io/errors"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

// safeValidatePublicKey safely validates any public key type to ensure it won't cause panics
// when used with sdk.GetConsAddress or other operations
func safeValidatePublicKey(pk cryptotypes.PubKey) error {
	if pk == nil {
		return errorsmod.Wrap(sdkerrors.ErrInvalidPubKey, "public key cannot be nil")
	}

	var err error

	// Use panic recovery to catch any panics from the Address() method
	defer func() {
		if r := recover(); r != nil {
			err = errorsmod.Wrapf(sdkerrors.ErrInvalidPubKey, "invalid public key format: %v", r)
		}
	}()

	// Test that the key works by calling Address() - this is where panics occur with invalid keys
	_ = pk.Address()

	return err
}

type msgServer struct {
	*Keeper
}

// NewMsgServerImpl returns an implementation of the staking MsgServer interface
// for the provided Keeper.
func NewMsgServerImpl(keeper *Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

var _ types.MsgServer = msgServer{}

// CreateValidator defines a method for creating a new validator
func (k msgServer) CreateValidator(ctx context.Context, msg *types.MsgCreateValidator) (*types.MsgCreateValidatorResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() > 1 {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "MsgCreateValidator is disabled after genesis")
	}

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

	if err := safeValidatePublicKey(pk); err != nil {
		return nil, err
	}

	consAddr := sdk.GetConsAddress(pk)
	locked, err := k.IsConsAddrLockedByRotation(ctx, consAddr)
	if err != nil {
		return nil, err
	}
	if locked {
		return nil, types.ErrConsensusPubKeyInRotationHistory
	}

	_, err = k.GetValidatorByConsAddr(ctx, consAddr)
	if err == nil {
		return nil, types.ErrValidatorPubKeyExists
	}
	if !errors.Is(err, types.ErrNoValidatorFound) {
		return nil, err
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

	cp := sdkCtx.ConsensusParams()
	if cp.Validator != nil {
		if err := types.ValidateConsensusPubKeyType(pk, cp.Validator.PubKeyTypes); err != nil {
			return nil, err
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

	err = k.SetValidator(ctx, validator)
	if err != nil {
		return nil, err
	}

	err = k.SetValidatorByConsAddr(ctx, validator)
	if err != nil {
		return nil, err
	}

	err = k.SetNewValidatorByPowerIndex(ctx, validator)
	if err != nil {
		return nil, err
	}

	// call the after-creation hook
	if err := k.Hooks().AfterValidatorCreated(ctx, valAddr); err != nil {
		return nil, err
	}

	// move coins from the msg.Address account to a (self-delegation) delegator account
	// the validator account and global shares are updated within here
	// NOTE source will always be from a wallet which are unbonded
	_, err = k.Keeper.Delegate(ctx, sdk.AccAddress(valAddr), msg.Value.Amount, types.Unbonded, validator, true)
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

// EditValidator defines a method for editing an existing validator
func (k msgServer) EditValidator(ctx context.Context, msg *types.MsgEditValidator) (*types.MsgEditValidatorResponse, error) {
	valAddr, err := k.validatorAddressCodec.StringToBytes(msg.ValidatorAddress)
	if err != nil {
		return nil, sdkerrors.ErrInvalidAddress.Wrapf("invalid validator address: %s", err)
	}

	if msg.Description == (types.Description{}) {
		return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "empty description")
	}

	// No-op for commission and min self delegation changes in proof of compute model
	// These parameters are ignored but we still validate basic input

	// validator must already be registered
	validator, err := k.GetValidator(ctx, valAddr)
	if err != nil {
		return nil, err
	}

	// replace all editable fields (clients should autofill existing values)
	description, err := validator.Description.UpdateDescription(msg.Description)
	if err != nil {
		return nil, err
	}

	validator.Description = description

	// In proof of compute model, only description changes are allowed
	// Commission and min self delegation changes are ignored

	err = k.SetValidator(ctx, validator)
	if err != nil {
		return nil, err
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.EventManager().EmitEvents(sdk.Events{
		sdk.NewEvent(
			types.EventTypeEditValidator,
			sdk.NewAttribute(types.AttributeKeyCommissionRate, validator.Commission.String()),
			sdk.NewAttribute(types.AttributeKeyMinSelfDelegation, validator.MinSelfDelegation.String()),
		),
	})

	return &types.MsgEditValidatorResponse{}, nil
}

// Delegate defines a method for performing a delegation of coins from a delegator to a validator
func (k msgServer) Delegate(ctx context.Context, msg *types.MsgDelegate) (*types.MsgDelegateResponse, error) {
	return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "Delegate is disabled")
}

func (k msgServer) BeginRedelegate(ctx context.Context, msg *types.MsgBeginRedelegate) (*types.MsgBeginRedelegateResponse, error) {
	return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "BeginRedelegate is disabled")
}

// Undelegate defines a method for performing an undelegation from a delegate and a validator
func (k msgServer) Undelegate(ctx context.Context, msg *types.MsgUndelegate) (*types.MsgUndelegateResponse, error) {
	return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "Undelegate is disabled")
}

// CancelUnbondingDelegation defines a method for canceling the unbonding delegation
// and delegate back to the validator.
func (k msgServer) CancelUnbondingDelegation(ctx context.Context, msg *types.MsgCancelUnbondingDelegation) (*types.MsgCancelUnbondingDelegationResponse, error) {
	return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "CancelUnbondingDelegation is disabled")
}

// UpdateParams defines a method to perform updation of params exist in x/staking module.
func (k msgServer) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if err := sdk.ValidateAuthority(sdkCtx, k.authority, msg.Authority); err != nil {
		return nil, err
	}

	if err := msg.Params.Validate(); err != nil {
		return nil, err
	}

	// Validate that the bond denom exists on-chain by checking if it has supply.
	// This prevents governance from setting bond_denom to a non-existent denom,
	// which would place the chain in an unsafe state.
	supply := k.bankKeeper.GetSupply(ctx, msg.Params.BondDenom)
	if supply.IsZero() {
		return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidRequest, "bond denom %s does not exist or has zero supply", msg.Params.BondDenom)
	}

	// store params
	if err := k.SetParams(ctx, msg.Params); err != nil {
		return nil, err
	}

	return &types.MsgUpdateParamsResponse{}, nil
}

// RotateConsPubKey defines a method for changing a validators consensus key to
// a new key.
func (k msgServer) RotateConsPubKey(ctx context.Context, msg *types.MsgRotateConsPubKey) (*types.MsgRotateConsPubKeyResponse, error) {
	return nil, errorsmod.Wrap(sdkerrors.ErrInvalidRequest, "RotateConsPubKey is disabled in Proof of Compute")
}
