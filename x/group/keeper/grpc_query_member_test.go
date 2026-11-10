package keeper_test

import (
	"fmt"
	"strconv"

	storetypes "cosmossdk.io/store/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/cosmos-sdk/x/group"
)

func (s *TestSuite) TestGetGroupMember() {
	const n = 29
	members := make([]group.MemberRequest, n)
	addrs := make([]sdk.AccAddress, n)
	for i := range members {
		addrs[i] = sdk.AccAddress(fmt.Sprintf("group-member-addr-%03d", i))
		members[i] = group.MemberRequest{Address: addrs[i].String(), Weight: strconv.Itoa(100 + i)}
	}
	res, err := s.groupKeeper.CreateGroup(s.ctx, &group.MsgCreateGroup{Admin: s.addrs[0].String(), Members: members})
	s.Require().NoError(err)

	pageCtx := s.sdkCtx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	all, err := s.groupKeeper.GroupMembers(pageCtx, &group.QueryGroupMembersRequest{
		GroupId: res.GroupId, Pagination: &query.PageRequest{Limit: 100},
	})
	s.Require().NoError(err)
	s.Require().Len(all.Members, n)

	getCtx := s.sdkCtx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	for i, addr := range addrs {
		m, err := s.groupKeeper.GetGroupMember(getCtx, res.GroupId, addr)
		s.Require().NoError(err)
		s.Require().Equal(all.Members[i], m)
	}
	// One keyed read costs a small fraction of paging the 29-member group.
	s.Require().Less(getCtx.GasMeter().GasConsumed()/n*10, pageCtx.GasMeter().GasConsumed())

	_, err = s.groupKeeper.GetGroupMember(s.ctx, res.GroupId, s.addrs[1])
	s.Require().ErrorIs(err, sdkerrors.ErrNotFound)
	_, err = s.groupKeeper.GetGroupMember(s.ctx, res.GroupId+1, addrs[0])
	s.Require().ErrorIs(err, sdkerrors.ErrNotFound)
	_, err = s.groupKeeper.GetGroupMember(s.ctx, res.GroupId, nil)
	s.Require().ErrorIs(err, sdkerrors.ErrInvalidAddress)

	// A removed member (weight 0) is gone from the group, as in GroupMembers.
	_, err = s.groupKeeper.UpdateGroupMembers(s.ctx, &group.MsgUpdateGroupMembers{
		Admin: s.addrs[0].String(), GroupId: res.GroupId,
		MemberUpdates: []group.MemberRequest{{Address: addrs[3].String(), Weight: "0"}},
	})
	s.Require().NoError(err)
	_, err = s.groupKeeper.GetGroupMember(s.ctx, res.GroupId, addrs[3])
	s.Require().ErrorIs(err, sdkerrors.ErrNotFound)
}
