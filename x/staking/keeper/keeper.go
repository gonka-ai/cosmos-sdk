package keeper

import (
	"context"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"

	addresscodec "cosmossdk.io/core/address"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log"
	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

// Implements ValidatorSet interface
var _ types.ValidatorSet = Keeper{}

// Implements DelegationSet interface
var _ types.DelegationSet = Keeper{}

// NoOpBankKeeper is a wrapper that makes all bank keeper operations no-ops
type NoOpBankKeeper struct {
	inner types.BankKeeper
}

// NewNoOpBankKeeper creates a new no-op bank keeper wrapper
func NewNoOpBankKeeper(bk types.BankKeeper) *NoOpBankKeeper {
	return &NoOpBankKeeper{inner: bk}
}

// Read operations delegate to the inner keeper
func (n *NoOpBankKeeper) GetAllBalances(ctx context.Context, addr sdk.AccAddress) sdk.Coins {
	return n.inner.GetAllBalances(ctx, addr)
}

func (n *NoOpBankKeeper) GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	return n.inner.GetBalance(ctx, addr, denom)
}

func (n *NoOpBankKeeper) LockedCoins(ctx context.Context, addr sdk.AccAddress) sdk.Coins {
	return n.inner.LockedCoins(ctx, addr)
}

func (n *NoOpBankKeeper) SpendableCoins(ctx context.Context, addr sdk.AccAddress) sdk.Coins {
	return n.inner.SpendableCoins(ctx, addr)
}

func (n *NoOpBankKeeper) GetSupply(ctx context.Context, denom string) sdk.Coin {
	return n.inner.GetSupply(ctx, denom)
}

func (n *NoOpBankKeeper) SendCoinsFromModuleToModule(ctx context.Context, senderPool, recipientPool string, amt sdk.Coins) error {
	return nil
}

func (n *NoOpBankKeeper) UndelegateCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error {
	return nil
}

func (n *NoOpBankKeeper) DelegateCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error {
	return nil
}

func (n *NoOpBankKeeper) BurnCoins(ctx context.Context, name string, amt sdk.Coins) error {
	return nil
}

// Keeper of the x/staking store
type Keeper struct {
	storeService          storetypes.KVStoreService
	cdc                   codec.BinaryCodec
	authKeeper            types.AccountKeeper
	bankKeeper            types.BankKeeper
	hooks                 types.StakingHooks
	authority             string
	validatorAddressCodec addresscodec.Codec
	consensusAddressCodec addresscodec.Codec
	// tombstoneChecker, when set, reports whether a validator is tombstoned in the
	// slashing module. Injected via SetTombstoneChecker to avoid a circular import.
	tombstoneChecker TombstoneChecker
}

// NewKeeper creates a new staking Keeper instance
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	ak types.AccountKeeper,
	bk types.BankKeeper,
	authority string,
	validatorAddressCodec addresscodec.Codec,
	consensusAddressCodec addresscodec.Codec,
) *Keeper {
	// ensure bonded and not bonded module accounts are set
	if addr := ak.GetModuleAddress(types.BondedPoolName); addr == nil {
		panic(fmt.Sprintf("%s module account has not been set", types.BondedPoolName))
	}

	if addr := ak.GetModuleAddress(types.NotBondedPoolName); addr == nil {
		panic(fmt.Sprintf("%s module account has not been set", types.NotBondedPoolName))
	}

	// ensure that authority is a valid AccAddress
	if _, err := ak.AddressCodec().StringToBytes(authority); err != nil {
		panic("authority is not a valid acc address")
	}

	if validatorAddressCodec == nil || consensusAddressCodec == nil {
		panic("validator and/or consensus address codec are nil")
	}

	// Wrap the bank keeper with no-op wrapper
	wrappedBankKeeper := NewNoOpBankKeeper(bk)

	return &Keeper{
		storeService:          storeService,
		cdc:                   cdc,
		authKeeper:            ak,
		bankKeeper:            wrappedBankKeeper,
		hooks:                 nil,
		authority:             authority,
		validatorAddressCodec: validatorAddressCodec,
		consensusAddressCodec: consensusAddressCodec,
	}
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx context.Context) log.Logger {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.Logger().With("module", "x/"+types.ModuleName)
}

// Hooks gets the hooks for staking *Keeper {
func (k *Keeper) Hooks() types.StakingHooks {
	if k.hooks == nil {
		// return a no-op implementation if no hooks are set
		return types.MultiStakingHooks{}
	}

	return k.hooks
}

// SetHooks sets the validator hooks.  In contrast to other receivers, this method must take a pointer due to nature
// of the hooks interface and SDK start up sequence.
func (k *Keeper) SetHooks(sh types.StakingHooks) {
	if k.hooks != nil {
		panic("cannot set validator hooks twice")
	}

	k.hooks = sh
}

// SetTombstoneChecker injects a function reporting whether a validator (by consensus
// address) is tombstoned in the slashing module. The epoch validator-set recompute
// (SetComputeValidators) uses it so a permanently-banned (e.g. equivocating) validator
// is not resurrected. Wired in app setup once the slashing keeper exists; nil = unchanged.
func (k *Keeper) SetTombstoneChecker(fn TombstoneChecker) {
	k.tombstoneChecker = fn
}

// GetLastTotalPower loads the last total validator power.
func (k Keeper) GetLastTotalPower(ctx context.Context) (math.Int, error) {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(types.LastTotalPowerKey)
	if err != nil {
		return math.ZeroInt(), err
	}

	if bz == nil {
		return math.ZeroInt(), nil
	}

	ip := sdk.IntProto{}
	err = k.cdc.Unmarshal(bz, &ip)
	if err != nil {
		return math.ZeroInt(), err
	}

	return ip.Int, nil
}

// SetLastTotalPower sets the last total validator power.
func (k Keeper) SetLastTotalPower(ctx context.Context, power math.Int) error {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := k.cdc.Marshal(&sdk.IntProto{Int: power})
	if err != nil {
		return err
	}
	return store.Set(types.LastTotalPowerKey, bz)
}

// GetAuthority returns the x/staking module's authority.
func (k Keeper) GetAuthority() string {
	return k.authority
}

// ValidatorAddressCodec returns the app validator address codec.
func (k Keeper) ValidatorAddressCodec() addresscodec.Codec {
	return k.validatorAddressCodec
}

// ConsensusAddressCodec returns the app consensus address codec.
func (k Keeper) ConsensusAddressCodec() addresscodec.Codec {
	return k.consensusAddressCodec
}

// SetValidatorUpdates sets the ABCI validator power updates for the current block.
func (k Keeper) SetValidatorUpdates(ctx context.Context, valUpdates []abci.ValidatorUpdate) error {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := k.cdc.Marshal(&types.ValidatorUpdates{Updates: valUpdates})
	if err != nil {
		return err
	}
	return store.Set(types.ValidatorUpdatesKey, bz)
}

// GetValidatorUpdates returns the ABCI validator power updates within the current block.
func (k Keeper) GetValidatorUpdates(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(types.ValidatorUpdatesKey)
	if err != nil {
		return nil, err
	}

	var valUpdates types.ValidatorUpdates
	err = k.cdc.Unmarshal(bz, &valUpdates)
	if err != nil {
		return nil, err
	}

	return valUpdates.Updates, nil
}
