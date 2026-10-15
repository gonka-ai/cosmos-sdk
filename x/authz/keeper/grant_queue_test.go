package keeper_test

import (
	"fmt"
	"time"

	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/x/authz"
	authzkeeper "github.com/cosmos/cosmos-sdk/x/authz/keeper"
)

func queueTypes(n int) []string {
	types := make([]string, n)
	for i := range types {
		types[i] = fmt.Sprintf("/inference.inference.MsgSubmitOperation%02d", i)
	}
	return types
}

// A grant tx with many msg types (a host granting its warm key) used to rewrite the growing
// GrantQueueItem once per type: the n-th grant paid for the n-1 types before it.
func (s *TestSuite) TestGrantQueueGasDoesNotGrowWithEarlierGrants() {
	granter, grantee := s.addrs[0], s.addrs[1]
	exp := s.ctx.BlockTime().Add(365 * 24 * time.Hour)
	ctx := s.ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())

	var perGrant []uint64
	for _, t := range queueTypes(15) {
		before := ctx.GasMeter().GasConsumed()
		s.Require().NoError(s.authzKeeper.SaveGrant(ctx, grantee, granter, authz.NewGenericAuthorization(t), &exp))
		perGrant = append(perGrant, ctx.GasMeter().GasConsumed()-before)
	}
	s.T().Logf("first grant %d gas, 15th %d, total %d", perGrant[0], perGrant[14], ctx.GasMeter().GasConsumed())
	s.Require().Equal(perGrant[0], perGrant[14])

	exp2 := exp.Add(time.Hour)
	ctx = s.ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	for _, t := range queueTypes(15) {
		s.Require().NoError(s.authzKeeper.SaveGrant(ctx, grantee, granter, authz.NewGenericAuthorization(t), &exp2))
	}
	s.T().Logf("re-grant with a new expiration: total %d", ctx.GasMeter().GasConsumed())

	s.Require().NoError(s.authzKeeper.DequeueAndDeleteExpiredGrants(s.ctx.WithBlockTime(exp2.Add(time.Second))))
	for _, t := range queueTypes(15) {
		auth, _ := s.authzKeeper.GetAuthorization(s.ctx, grantee, granter, t)
		s.Require().Nil(auth)
	}
	s.Require().Empty(s.queueKeys())
}

// Queues written before per-grant entries hold a GrantQueueItem list; grants queued that way
// must still be re-granted, revoked and pruned.
func (s *TestSuite) TestLegacyGrantQueueItem() {
	granter, grantee := s.addrs[0], s.addrs[1]
	exp := s.ctx.BlockTime().Add(time.Hour)
	types := queueTypes(3)
	for _, t := range types {
		s.Require().NoError(s.authzKeeper.SaveGrant(s.ctx, grantee, granter, authz.NewGenericAuthorization(t), &exp))
	}
	s.toLegacyQueue(exp, types)

	// types[0]: re-granted with a later expiration, leaves the legacy list.
	later := exp.Add(time.Hour)
	s.Require().NoError(s.authzKeeper.SaveGrant(s.ctx, grantee, granter, authz.NewGenericAuthorization(types[0]), &later))
	// types[1]: revoked.
	s.Require().NoError(s.authzKeeper.DeleteGrant(s.ctx, grantee, granter, types[1]))

	var item authz.GrantQueueItem
	bz := s.ctx.KVStore(s.storeKey).Get(authzkeeper.GrantQueueKey(exp, granter, grantee))
	s.Require().NoError(s.encCfg.Codec.Unmarshal(bz, &item))
	s.Require().Equal([]string{types[2]}, item.MsgTypeUrls)

	// types[2] expires from the legacy list, types[0] stays until its own expiration.
	s.Require().NoError(s.authzKeeper.DequeueAndDeleteExpiredGrants(s.ctx.WithBlockTime(exp.Add(time.Second))))
	auth, _ := s.authzKeeper.GetAuthorization(s.ctx, grantee, granter, types[2])
	s.Require().Nil(auth)
	auth, _ = s.authzKeeper.GetAuthorization(s.ctx, grantee, granter, types[0])
	s.Require().NotNil(auth)

	s.Require().NoError(s.authzKeeper.DequeueAndDeleteExpiredGrants(s.ctx.WithBlockTime(later.Add(time.Second))))
	auth, _ = s.authzKeeper.GetAuthorization(s.ctx, grantee, granter, types[0])
	s.Require().Nil(auth)
	s.Require().Empty(s.queueKeys())
}

// toLegacyQueue replaces the per-grant queue entries of types with one GrantQueueItem list,
// the layout every queued grant on chain has before this change.
func (s *TestSuite) toLegacyQueue(exp time.Time, types []string) {
	store := s.ctx.KVStore(s.storeKey)
	granter, grantee := s.addrs[0], s.addrs[1]
	base := authzkeeper.GrantQueueKey(exp, granter, grantee)
	for _, t := range types {
		key := append(append([]byte{}, base...), t...)
		s.Require().True(store.Has(key))
		store.Delete(key)
	}
	bz, err := s.encCfg.Codec.Marshal(&authz.GrantQueueItem{MsgTypeUrls: types})
	s.Require().NoError(err)
	store.Set(base, bz)
}

func (s *TestSuite) queueKeys() [][]byte {
	it := storetypes.KVStorePrefixIterator(s.ctx.KVStore(s.storeKey), authzkeeper.GrantQueuePrefix)
	defer it.Close()
	var keys [][]byte
	for ; it.Valid(); it.Next() {
		if len(it.Value()) > 0 {
			var item authz.GrantQueueItem
			s.Require().NoError(s.encCfg.Codec.Unmarshal(it.Value(), &item))
			if len(item.MsgTypeUrls) == 0 {
				continue // a drained legacy list
			}
		}
		keys = append(keys, it.Key())
	}
	return keys
}
