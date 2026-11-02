package server

import (
	"context"

	abcicli "github.com/cometbft/cometbft/abci/client"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtsync "github.com/cometbft/cometbft/libs/sync"
	"github.com/cometbft/cometbft/proxy"

	"github.com/cosmos/cosmos-sdk/baseapp"
)

// newQueryUnlockedClientCreator keeps one ABCI mutex for all connections, but
// lets queries that go through CreateQueryContext bypass it, as gRPC does.
func newQueryUnlockedClientCreator(app abci.Application) proxy.ClientCreator {
	return &queryUnlockedCreator{mtx: new(cmtsync.Mutex), app: app}
}

type queryUnlockedCreator struct {
	mtx *cmtsync.Mutex
	app abci.Application
}

func (c *queryUnlockedCreator) NewABCIClient() (abcicli.Client, error) {
	return &queryUnlockedClient{Client: abcicli.NewLocalClient(c.mtx, c.app), app: c.app}, nil
}

type queryUnlockedClient struct {
	abcicli.Client
	app abci.Application
}

// cmtABCIQuery forwards store/app/p2p paths to BaseApp.Query.
const cmtABCIQuery = "/cosmos.base.tendermint.v1beta1.Service/ABCIQuery"

// Query keeps the lock for store (live IAVL tree, no gas limit) and p2p paths.
// app/simulate runs on checkState without a gas limit, as on :9090; under the
// lock a flood of it stalls block commits.
func (c *queryUnlockedClient) Query(ctx context.Context, req *abci.RequestQuery) (*abci.ResponseQuery, error) {
	path := baseapp.SplitABCIQueryPath(req.Path)
	if len(path) == 0 || req.Path == cmtABCIQuery {
		return c.Client.Query(ctx, req)
	}
	switch path[0] {
	case "store", "p2p":
		return c.Client.Query(ctx, req)
	}
	return c.app.Query(ctx, req)
}
