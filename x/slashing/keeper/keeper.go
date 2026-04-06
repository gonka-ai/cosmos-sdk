package keeper

import (
	"context"
	"fmt"

	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// MaintenanceChecker is an optional interface that, when set, allows the
// slashing keeper to skip missed-block accounting and downtime jailing for
// validators that are currently in a scheduled maintenance window.
// This is injected after construction via SetMaintenanceChecker to avoid
// import cycles between the slashing module and the application-level
// maintenance state owner.
type MaintenanceChecker interface {
	IsValidatorInActiveMaintenance(ctx context.Context, consAddr sdk.ConsAddress) bool
}

// Keeper of the slashing store
type Keeper struct {
	storeService storetypes.KVStoreService
	cdc          codec.BinaryCodec
	legacyAmino  *codec.LegacyAmino
	sk           types.StakingKeeper

	// the address capable of executing a MsgUpdateParams message. Typically, this
	// should be the x/gov module account.
	authority string

	// maintenanceChecker is an optional checker for maintenance window exemptions.
	// When non-nil, validators in active maintenance skip liveness accounting.
	maintenanceChecker MaintenanceChecker
}

// NewKeeper creates a slashing keeper
func NewKeeper(cdc codec.BinaryCodec, legacyAmino *codec.LegacyAmino, storeService storetypes.KVStoreService, sk types.StakingKeeper, authority string) Keeper {
	return Keeper{
		storeService: storeService,
		cdc:          cdc,
		legacyAmino:  legacyAmino,
		sk:           sk,
		authority:    authority,
	}
}

// SetMaintenanceChecker sets the optional maintenance checker used to exempt
// validators in active maintenance from downtime liveness accounting.
func (k *Keeper) SetMaintenanceChecker(mc MaintenanceChecker) {
	k.maintenanceChecker = mc
}

// GetAuthority returns the x/slashing module's authority.
func (k Keeper) GetAuthority() string {
	return k.authority
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx context.Context) log.Logger {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.Logger().With("module", "x/"+types.ModuleName)
}

// RestoreValidatorIndex exposes the underlying staking keeper's RestoreValidatorIndex.
func (k Keeper) RestoreValidatorIndex(ctx context.Context) {
	k.sk.RestoreValidatorIndex(ctx)
}

// AddPubkey sets a address-pubkey relation
func (k Keeper) AddPubkey(ctx context.Context, pubkey cryptotypes.PubKey) error {
	bz, err := k.cdc.MarshalInterface(pubkey)
	if err != nil {
		return err
	}
	store := k.storeService.OpenKVStore(ctx)
	key := types.AddrPubkeyRelationKey(pubkey.Address())
	return store.Set(key, bz)
}

// GetPubkey returns the pubkey from the adddress-pubkey relation
func (k Keeper) GetPubkey(ctx context.Context, a cryptotypes.Address) (cryptotypes.PubKey, error) {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(types.AddrPubkeyRelationKey(a))
	if err != nil {
		return nil, err
	}
	if bz == nil {
		return nil, fmt.Errorf("address %s not found", sdk.ConsAddress(a))
	}
	var pk cryptotypes.PubKey
	return pk, k.cdc.UnmarshalInterface(bz, &pk)
}

// Slash attempts to slash a validator. The slash is delegated to the staking
// module to make the necessary validator changes. It specifies no intraction reason.
func (k Keeper) Slash(ctx context.Context, consAddr sdk.ConsAddress, fraction sdkmath.LegacyDec, power, distributionHeight int64) error {
	return k.SlashWithInfractionReason(ctx, consAddr, fraction, power, distributionHeight, stakingtypes.Infraction_INFRACTION_UNSPECIFIED)
}

// SlashWithInfractionReason attempts to slash a validator. The slash is delegated to the staking
// module to make the necessary validator changes. It specifies an intraction reason.
func (k Keeper) SlashWithInfractionReason(ctx context.Context, consAddr sdk.ConsAddress, fraction sdkmath.LegacyDec, power, distributionHeight int64, infraction stakingtypes.Infraction) error {
	coinsBurned, err := k.sk.SlashWithInfractionReason(ctx, consAddr, distributionHeight, power, fraction, infraction)
	if err != nil {
		return err
	}

	reasonAttr := sdk.NewAttribute(types.AttributeKeyReason, types.AttributeValueUnspecified)
	switch infraction {
	case stakingtypes.Infraction_INFRACTION_DOUBLE_SIGN:
		reasonAttr = sdk.NewAttribute(types.AttributeKeyReason, types.AttributeValueDoubleSign)
	case stakingtypes.Infraction_INFRACTION_DOWNTIME:
		reasonAttr = sdk.NewAttribute(types.AttributeKeyReason, types.AttributeValueMissingSignature)
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeSlash,
			sdk.NewAttribute(types.AttributeKeyAddress, consAddr.String()),
			sdk.NewAttribute(types.AttributeKeyPower, fmt.Sprintf("%d", power)),
			reasonAttr,
			sdk.NewAttribute(types.AttributeKeyBurnedCoins, coinsBurned.String()),
		),
	)
	return nil
}

// Jail attempts to jail a validator. The slash is delegated to the staking module
// to make the necessary validator changes.
func (k Keeper) Jail(ctx context.Context, consAddr sdk.ConsAddress) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	if err := k.sk.Jail(sdkCtx, consAddr); err != nil {
		return fmt.Errorf("slashing validator jail: %w", err)
	}

	sdkCtx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeSlash,
			sdk.NewAttribute(types.AttributeKeyJailed, consAddr.String()),
		),
	)
	return nil
}

func (k Keeper) deleteAddrPubkeyRelation(ctx context.Context, addr cryptotypes.Address) error {
	store := k.storeService.OpenKVStore(ctx)
	return store.Delete(types.AddrPubkeyRelationKey(addr))
}
