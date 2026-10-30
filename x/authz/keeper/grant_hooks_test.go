package keeper_test

import (
	"context"
	"errors"
	"time"

	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

type grantHooks struct {
	saved   int
	deleted int
	msgType string
	exp     *time.Time
	saveErr error
	delErr  error
}

func (h *grantHooks) AfterGrantSaved(_ context.Context, _, _ sdk.AccAddress, msgTypeURL string, expiration *time.Time) error {
	h.saved++
	h.msgType = msgTypeURL
	h.exp = expiration
	return h.saveErr
}

func (h *grantHooks) AfterGrantDeleted(_ context.Context, _, _ sdk.AccAddress, _ string) error {
	h.deleted++
	return h.delErr
}

func (s *TestSuite) TestGrantHooksSaveAndDelete() {
	ctx, addrs := s.ctx, s.addrs
	expire := ctx.BlockTime().Add(time.Hour)
	hooks := &grantHooks{}
	copied := s.authzKeeper
	s.authzKeeper.SetGrantHooks(hooks)

	send := &banktypes.SendAuthorization{SpendLimit: coins10}
	s.Require().NoError(copied.SaveGrant(ctx, addrs[1], addrs[0], send, &expire))
	s.Require().Equal(1, hooks.saved)
	s.Require().Equal(bankSendAuthMsgType, hooks.msgType)
	s.Require().Equal(&expire, hooks.exp)
	s.Require().Empty(hooks.deleted)

	s.Require().NoError(copied.DeleteGrant(ctx, addrs[1], addrs[0], bankSendAuthMsgType))
	s.Require().Equal(1, hooks.deleted)

	err := copied.DeleteGrant(ctx, addrs[1], addrs[0], bankSendAuthMsgType)
	s.Require().Error(err)
	s.Require().Equal(1, hooks.deleted, "a missing grant does not call the hook")
}

func (s *TestSuite) TestGrantHooksNotCalledForExpiry() {
	ctx, addrs := s.ctx, s.addrs
	expire := ctx.BlockTime().Add(time.Hour)
	hooks := &grantHooks{}
	s.authzKeeper.SetGrantHooks(hooks)

	send := &banktypes.SendAuthorization{SpendLimit: coins10}
	s.Require().NoError(s.authzKeeper.SaveGrant(ctx, addrs[1], addrs[0], send, &expire))
	s.Require().Equal(1, hooks.saved)

	later := ctx.WithBlockTime(expire.Add(time.Second))
	s.Require().NoError(s.authzKeeper.DequeueAndDeleteExpiredGrants(later))
	s.Require().Zero(hooks.deleted)
	authzs, err := s.authzKeeper.GetAuthorizations(later, addrs[1], addrs[0])
	s.Require().NoError(err)
	s.Require().Empty(authzs)
}

func (s *TestSuite) TestGrantHooksErrorFromMsgServer() {
	ctx := s.ctx.WithBlockTime(time.Now())
	s.createAccounts(2)
	hooks := &grantHooks{saveErr: errors.New("save failed"), delErr: errors.New("delete failed")}
	s.authzKeeper.SetGrantHooks(hooks)

	coins := sdk.NewCoins(sdk.NewCoin("steak", sdkmath.NewInt(10)))
	grant, err := authz.NewMsgGrant(s.addrs[0], s.addrs[1], &banktypes.SendAuthorization{SpendLimit: coins}, nil)
	s.Require().NoError(err)
	_, err = s.msgSrvr.Grant(ctx, grant)
	s.Require().ErrorContains(err, "save failed")

	hooks.saveErr = nil
	_, err = s.msgSrvr.Grant(ctx, grant)
	s.Require().NoError(err)

	revoke := authz.NewMsgRevoke(s.addrs[0], s.addrs[1], bankSendAuthMsgType)
	_, err = s.msgSrvr.Revoke(ctx, &revoke)
	s.Require().ErrorContains(err, "delete failed")
}

func (s *TestSuite) TestSetGrantHooksTwicePanics() {
	s.authzKeeper.SetGrantHooks(&grantHooks{})
	require.Panics(s.T(), func() { s.authzKeeper.SetGrantHooks(&grantHooks{}) })
}
