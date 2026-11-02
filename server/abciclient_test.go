package server

import (
	"context"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"
)

type blockingApp struct {
	abci.BaseApplication
	entered, release chan struct{}
}

func (a *blockingApp) FinalizeBlock(context.Context, *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	close(a.entered)
	<-a.release
	return &abci.ResponseFinalizeBlock{}, nil
}

func TestQueryUnlockedClientCreator(t *testing.T) {
	app := &blockingApp{entered: make(chan struct{}), release: make(chan struct{})}
	creator := newQueryUnlockedClientCreator(app)
	consensus, err := creator.NewABCIClient()
	require.NoError(t, err)
	mempool, err := creator.NewABCIClient()
	require.NoError(t, err)
	query, err := creator.NewABCIClient()
	require.NoError(t, err)

	go func() { _, _ = consensus.FinalizeBlock(context.Background(), &abci.RequestFinalizeBlock{}) }()
	<-app.entered

	queried := make(chan struct{})
	go func() {
		_, _ = query.Query(context.Background(), &abci.RequestQuery{Path: "/cosmos.bank.v1beta1.Query/Balance"})
		_, _ = query.Query(context.Background(), &abci.RequestQuery{Path: "/app/simulate"})
		_, _ = query.Query(context.Background(), &abci.RequestQuery{Path: "app/version"})
		close(queried)
	}()
	var locked []chan struct{}
	for _, path := range []string{"/store/bank/subspace", "store/bank/subspace", "p2p/filter/id/x", "", cmtABCIQuery} {
		done := make(chan struct{})
		locked = append(locked, done)
		go func() { _, _ = query.Query(context.Background(), &abci.RequestQuery{Path: path}); close(done) }()
	}
	checked := make(chan struct{})
	go func() { _, _ = mempool.CheckTx(context.Background(), &abci.RequestCheckTx{}); close(checked) }()

	select {
	case <-queried:
	case <-time.After(5 * time.Second):
		t.Fatal("Query waited for FinalizeBlock")
	}
	time.Sleep(100 * time.Millisecond)
	for _, done := range append(locked, checked) {
		select {
		case <-done:
			t.Fatal("CheckTx or a store/p2p query ran during FinalizeBlock")
		default:
		}
	}

	close(app.release)
	for _, done := range append(locked, checked) {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("CheckTx or a locked query did not run after FinalizeBlock")
		}
	}
}
