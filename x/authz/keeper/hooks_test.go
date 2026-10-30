package keeper

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

func TestNilGrantHooksSlot(t *testing.T) {
	var k Keeper
	require.NoError(t, k.afterGrantSaved(context.Background(), nil, nil, "/cosmos.bank.v1beta1.MsgSend", nil))
	require.NoError(t, k.afterGrantDeleted(context.Background(), nil, nil, "/cosmos.bank.v1beta1.MsgSend"))
	require.Panics(t, func() { k.SetGrantHooks(noopHooks{}) })
}

type noopHooks struct{}

func (noopHooks) AfterGrantSaved(context.Context, sdk.AccAddress, sdk.AccAddress, string, *time.Time) error {
	return nil
}

func (noopHooks) AfterGrantDeleted(context.Context, sdk.AccAddress, sdk.AccAddress, string) error {
	return nil
}
