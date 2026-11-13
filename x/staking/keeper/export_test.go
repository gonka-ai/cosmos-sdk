package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

// DeleteValidatorInternalForTest exposes the unexported deleteValidatorInternal
// method for use by black-box tests in package keeper_test. It is only compiled
// during tests (file ends in _test.go) and does not appear in the public API.
func (k Keeper) DeleteValidatorInternalForTest(ctx context.Context, validator types.Validator, valAddr sdk.ValAddress) error {
	return k.deleteValidatorInternal(ctx, validator, valAddr)
}
