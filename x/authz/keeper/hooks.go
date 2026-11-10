package keeper

import (
	"context"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// GrantHooks is called after SaveGrant stores a grant and after DeleteGrant
// removes one. DequeueAndDeleteExpiredGrants does not call it: expiry deletes
// the store key itself. update does not call it either: it rewrites the
// authorization of an existing grant and keeps the grant and its expiration.
// An error is returned from SaveGrant or DeleteGrant;
// the caller's context must be discarded, as for any failed message.
//
// The hook sees only the grant being written. It is not a place to scan the
// grant queue or the live set.
type GrantHooks interface {
	AfterGrantSaved(ctx context.Context, grantee, granter sdk.AccAddress, msgTypeURL string, expiration *time.Time) error
	AfterGrantDeleted(ctx context.Context, grantee, granter sdk.AccAddress, msgTypeURL string) error
}

// grantHooksSlot is shared by every copy of a keeper. Keeper is a value, and
// ProvideModule registers one copy as the message server while the app holds
// another. A hook stored on the value would not reach the copy behind the
// router.
type grantHooksSlot struct{ hooks GrantHooks }

// SetGrantHooks installs the hooks on every copy of this keeper. A keeper
// built without NewKeeper has no slot. A second call panics.
func (k Keeper) SetGrantHooks(h GrantHooks) {
	if k.grantHooks == nil {
		panic("authz: keeper was not constructed with NewKeeper")
	}
	if h == nil {
		panic("authz: nil grant hooks")
	}
	if k.grantHooks.hooks != nil {
		panic("authz: grant hooks already set")
	}
	k.grantHooks.hooks = h
}

func (k Keeper) afterGrantSaved(ctx context.Context, grantee, granter sdk.AccAddress, msgTypeURL string, expiration *time.Time) error {
	if k.grantHooks == nil || k.grantHooks.hooks == nil {
		return nil
	}
	return k.grantHooks.hooks.AfterGrantSaved(ctx, grantee, granter, msgTypeURL, expiration)
}

func (k Keeper) afterGrantDeleted(ctx context.Context, grantee, granter sdk.AccAddress, msgTypeURL string) error {
	if k.grantHooks == nil || k.grantHooks.hooks == nil {
		return nil
	}
	return k.grantHooks.hooks.AfterGrantDeleted(ctx, grantee, granter, msgTypeURL)
}
