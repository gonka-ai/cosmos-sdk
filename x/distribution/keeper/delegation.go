package keeper

import (
	"bytes"
	"context"
	"fmt"

	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

func (k Keeper) initializeDelegation(ctx context.Context, val sdk.ValAddress, del sdk.AccAddress) error {
	validator, err := k.stakingKeeper.Validator(ctx, val)
	if err != nil {
		return err
	}
	return k.SetDelegatorStartingInfo(
		ctx, val, del, types.NewDelegatorStartingInfo(0, validator.GetTokens().ToLegacyDec(), 0))
}

func (k Keeper) CalculateDelegationRewards(ctx context.Context, val stakingtypes.ValidatorI, del stakingtypes.DelegationI, endingPeriod uint64) (rewards sdk.DecCoins, err error) {
	return sdk.DecCoins{}, nil
}

// withdrawDestination is the resolved target for a reward withdrawal.
// String yields the value used for the withdraw_address event attribute.
type withdrawDestination interface {
	IsRedirected() bool
	SpecifiedWithdrawAddress() string
	ResolvedWithdrawAddress() string
}

// withdrawToAddr routes rewards to a specific account via the bank module.
type withdrawToAddr struct {
	ResolvedWithdrawAddr  sdk.AccAddress
	SpecifiedWithdrawAddr sdk.AccAddress
}

func newWithdrawToAddress(resolved, specified sdk.AccAddress) withdrawToAddr {
	return withdrawToAddr{
		ResolvedWithdrawAddr:  resolved,
		SpecifiedWithdrawAddr: specified,
	}
}

func (d withdrawToAddr) IsRedirected() bool {
	return !bytes.Equal(d.ResolvedWithdrawAddr, d.SpecifiedWithdrawAddr)
}

func (d withdrawToAddr) SpecifiedWithdrawAddress() string {
	return d.SpecifiedWithdrawAddr.String()
}

func (d withdrawToAddr) ResolvedWithdrawAddress() string {
	return d.ResolvedWithdrawAddr.String()
}

// withdrawToCommunityPool credits rewards to the community pool.
type withdrawToCommunityPool struct {
	SpecifiedWithdrawAddr sdk.AccAddress
}

func newWithdrawToCommunityPool(specified sdk.AccAddress) withdrawToCommunityPool {
	return withdrawToCommunityPool{SpecifiedWithdrawAddr: specified}
}

func (withdrawToCommunityPool) IsRedirected() bool {
	return true
}

func (d withdrawToCommunityPool) SpecifiedWithdrawAddress() string {
	return d.SpecifiedWithdrawAddr.String()
}

func (withdrawToCommunityPool) ResolvedWithdrawAddress() string {
	return types.AttributeValueCommunityPool
}

// resolveWithdrawDestination resolves a withdraw destination based on if the
// withdraw is 'strict' or not. a withdraw being strict or not determines if
// fallback addresses should be used or not. if a withdraw is strict, the
// withdraw must go to the owners specified withdraw address, and if it cannot
// for some reason, an error is returned. if a withdraw is not strict and it
// cannot go to the owner's specified withdraw address, it will fallback to the
// owner's address itself. if the owner's address cannot be used, it will
// fallback to the community pool.
func (k Keeper) resolveWithdrawDestination(
	ctx context.Context,
	owner sdk.AccAddress,
	strict bool,
) (withdrawDestination, error) {
	if strict {
		return k.resolveWithdrawDestinationStrict(ctx, owner)
	}
	return k.resolveWithdrawDestinationFallback(ctx, owner)
}

// resolveWithdrawDestinationStrict returns the destination for owner's stored
// withdraw address. If that address is in the bank module's blocked set, it
// returns ErrWithdrawAddrBlocked.
func (k Keeper) resolveWithdrawDestinationStrict(
	ctx context.Context,
	owner sdk.AccAddress,
) (withdrawToAddr, error) {
	withdrawAddr, err := k.GetDelegatorWithdrawAddr(ctx, owner)
	if err != nil {
		return withdrawToAddr{}, err
	}
	if k.bankKeeper.BlockedAddr(withdrawAddr) {
		// copying error return that the bank module uses when it tries to send
		// to a blocked address
		return withdrawToAddr{}, errorsmod.Wrapf(sdkerrors.ErrUnauthorized, "%s is not allowed to receive funds", withdrawAddr)
	}
	return newWithdrawToAddress(withdrawAddr, withdrawAddr), nil
}

// resolveWithdrawDestinationFallback returns the destination for owner's
// stored withdraw address. If that address is in the bank module's blocked
// set, it falls back to the owner's address itself. If the owner's address is
// in the bank module's blocked set, it falls back to the community pool.
func (k Keeper) resolveWithdrawDestinationFallback(
	ctx context.Context,
	owner sdk.AccAddress,
) (withdrawDestination, error) {
	withdrawAddr, err := k.GetDelegatorWithdrawAddr(ctx, owner)
	if err != nil {
		return nil, err
	}

	if !k.bankKeeper.BlockedAddr(withdrawAddr) {
		return newWithdrawToAddress(withdrawAddr, withdrawAddr), nil
	}
	if !k.bankKeeper.BlockedAddr(owner) {
		return newWithdrawToAddress(owner, withdrawAddr), nil
	}
	return newWithdrawToCommunityPool(withdrawAddr), nil
}

func emitWithdrawDestinationRedirectedEvent(ctx context.Context, dest withdrawDestination, validatorOp, delegatorAddr string) {
	attrs := []sdk.Attribute{
		sdk.NewAttribute(types.AttributeKeyOriginalWithdrawAddress, dest.SpecifiedWithdrawAddress()),
		sdk.NewAttribute(types.AttributeKeyWithdrawAddress, dest.ResolvedWithdrawAddress()),
		sdk.NewAttribute(types.AttributeKeyValidator, validatorOp),
	}
	if delegatorAddr != "" {
		attrs = append(attrs, sdk.NewAttribute(types.AttributeKeyDelegator, delegatorAddr))
	}
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(types.EventTypeWithdrawAddrRedirected, attrs...))
}

func (k Keeper) withdrawDelegationRewards(ctx context.Context, val stakingtypes.ValidatorI, del stakingtypes.DelegationI, dest withdrawDestination) (sdk.Coins, error) {
	return sdk.Coins{}, nil
}

func (k Keeper) sendCoinsToDestination(ctx context.Context, coins sdk.DecCoins, dest withdrawDestination) (sdk.Coins, error) {
	toSend, remainder := coins.TruncateDecimal()

	communityPoolFunds := remainder
	switch d := dest.(type) {
	case withdrawToAddr:
		if !toSend.IsZero() {
			if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, d.ResolvedWithdrawAddr, toSend); err != nil {
				return nil, err
			}
		}
	case withdrawToCommunityPool:
		communityPoolFunds = communityPoolFunds.Add(sdk.NewDecCoinsFromCoins(toSend...)...)
	default:
		return nil, fmt.Errorf("unknown withdraw destination type %T", dest)
	}

	if !communityPoolFunds.IsZero() {
		feePool, err := k.FeePool.Get(ctx)
		if err != nil {
			return nil, err
		}
		feePool.CommunityPool = feePool.CommunityPool.Add(communityPoolFunds...)
		if err := k.FeePool.Set(ctx, feePool); err != nil {
			return nil, err
		}
	}

	return toSend, nil
}
