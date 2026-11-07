package tx_test

import (
	"context"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/testutil"
	"github.com/cosmos/cosmos-sdk/std"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
)

type blockClient struct {
	client.CometRPC
	block *cmttypes.Block
}

func (c blockClient) Block(_ context.Context, _ *int64) (*coretypes.ResultBlock, error) {
	return &coretypes.ResultBlock{Block: c.block}, nil
}

// Test flow:
// 1. Build a tx server backed by a fake node that serves a block with two txs.
// 2. Call GetBlockWithTxs with a pagination limit of 1<<40.
// 3. Check the call returns within a second without error and the result holds only the block's txs.
func TestGetBlockWithTxsHugeLimit(t *testing.T) {
	interfaceRegistry := testutil.CodecOptions{}.NewInterfaceRegistry()
	std.RegisterInterfaces(interfaceRegistry)
	txConfig := tx.NewTxConfig(codec.NewProtoCodec(interfaceRegistry), tx.DefaultSignModes)

	txBytes, err := txConfig.TxEncoder()(txConfig.NewTxBuilder().GetTx())
	require.NoError(t, err)
	block := &cmttypes.Block{Data: cmttypes.Data{Txs: cmttypes.Txs{txBytes, txBytes}}}

	clientCtx := client.Context{}.WithTxConfig(txConfig).WithClient(blockClient{block: block})
	srv := tx.NewTxServer(clientCtx, nil, interfaceRegistry)
	ctx := sdk.NewContext(nil, cmtproto.Header{Height: 1}, false, log.NewNopLogger())

	done := make(chan struct{})
	var res *txtypes.GetBlockWithTxsResponse
	go func() {
		defer close(done)
		res, err = srv.GetBlockWithTxs(ctx, &txtypes.GetBlockWithTxsRequest{
			Height:     1,
			Pagination: &query.PageRequest{Limit: 1 << 40},
		})
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for GetBlockWithTxs to return")
	}
	require.NoError(t, err)
	require.Len(t, res.Txs, 2)
	require.Equal(t, uint64(2), res.Pagination.Total)
}
