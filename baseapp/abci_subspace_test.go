package baseapp_test

import (
	"context"
	"fmt"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/baseapp"
	baseapptestutil "github.com/cosmos/cosmos-sdk/baseapp/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

func subspaceSuite(t *testing.T, writes map[int64][]string, opts ...func(*baseapp.BaseApp)) *BaseAppSuite {
	t.Helper()
	anteOpt := func(bapp *baseapp.BaseApp) {
		bapp.SetAnteHandler(func(ctx sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
			if !simulate && ctx.ExecMode() == sdk.ExecModeFinalize {
				for _, k := range writes[ctx.BlockHeight()] {
					ctx.KVStore(capKey1).Set([]byte(k), []byte("v-"+k))
				}
			}
			return ctx, nil
		})
	}
	suite := NewBaseAppSuite(t, append([]func(*baseapp.BaseApp){anteOpt}, opts...)...)
	baseapptestutil.RegisterCounterServer(suite.baseApp.MsgServiceRouter(), CounterServerImplGasMeterOnly{})
	_, err := suite.baseApp.InitChain(&abci.RequestInitChain{ConsensusParams: &cmtproto.ConsensusParams{}})
	require.NoError(t, err)
	return suite
}

func finalize(t *testing.T, suite *BaseAppSuite, height int64, commit bool) {
	t.Helper()
	bz, err := suite.txConfig.TxEncoder()(newTxCounter(t, suite.txConfig, height, 0))
	require.NoError(t, err)
	_, err = suite.baseApp.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Txs: [][]byte{bz}})
	require.NoError(t, err)
	if commit {
		_, err = suite.baseApp.Commit()
		require.NoError(t, err)
	}
}

func TestABCI_QueryStoreSubspace(t *testing.T) {
	var block1 []string
	for i := 0; i < 50; i++ {
		block1 = append(block1, fmt.Sprintf("p/%02d", i))
	}
	block1 = append(block1, "q/outside")
	suite := subspaceSuite(t, map[int64][]string{1: block1, 2: {"p/uncommitted"}})
	finalize(t, suite, 1, true)

	// Same bytes as the IAVL store's own subspace query.
	want, err := suite.baseApp.CommitMultiStore().(storetypes.Queryable).Query(
		&storetypes.RequestQuery{Path: "/key1/subspace", Data: []byte("p/"), Height: 1})
	require.NoError(t, err)
	require.NotEmpty(t, want.Value)
	for _, path := range []string{"/store/key1/subspace", "store/key1/subspace"} {
		res, err := suite.baseApp.Query(context.TODO(), &abci.RequestQuery{Path: path, Data: []byte("p/")})
		require.NoError(t, err)
		require.Equal(t, uint32(0), res.Code, res.Log)
		require.Equal(t, want.Value, res.Value)
		require.Equal(t, []byte("p/"), res.Key)
		require.Equal(t, int64(1), res.Height)
	}

	// Written by FinalizeBlock but not committed: not part of height 1.
	finalize(t, suite, 2, false)
	res, err := suite.baseApp.Query(context.TODO(), &abci.RequestQuery{Path: "/store/key1/subspace", Data: []byte("p/"), Height: 1})
	require.NoError(t, err)
	require.Equal(t, want.Value, res.Value)

	res, err = suite.baseApp.Query(context.TODO(), &abci.RequestQuery{Path: "/store/key1/subspace", Data: []byte("none/")})
	require.NoError(t, err)
	require.Equal(t, uint32(0), res.Code, res.Log)
	require.NotNil(t, res.Value)
	require.Empty(t, res.Value)

	// Unchanged errors: empty prefix, unknown store.
	res, err = suite.baseApp.Query(context.TODO(), &abci.RequestQuery{Path: "/store/key1/subspace"})
	require.NoError(t, err)
	require.NotEqual(t, uint32(0), res.Code)
	res, err = suite.baseApp.Query(context.TODO(), &abci.RequestQuery{Path: "/store/nostore/subspace", Data: []byte("p/")})
	require.NoError(t, err)
	require.NotEqual(t, uint32(0), res.Code)
	res, err = suite.baseApp.Query(context.TODO(), &abci.RequestQuery{Path: "/store/key1/subspace", Data: []byte("p/"), Height: 1, Prove: true})
	require.NoError(t, err)
	require.Equal(t, sdkerrors.ErrInvalidRequest.ABCICode(), res.Code, res.Log)
}

func TestABCI_QueryStoreSubspaceGasLimit(t *testing.T) {
	var block1 []string
	for i := 0; i < 200; i++ {
		block1 = append(block1, fmt.Sprintf("p/%03d", i))
	}
	suite := subspaceSuite(t, map[int64][]string{1: block1}, baseapp.SetQueryGasLimit(5000))
	finalize(t, suite, 1, true)

	res, err := suite.baseApp.Query(context.TODO(), &abci.RequestQuery{Path: "/store/key1/subspace", Data: []byte("p/")})
	require.NoError(t, err)
	require.Equal(t, sdkerrors.ErrOutOfGas.ABCICode(), res.Code, res.Log)
	require.Empty(t, res.Value)

	// A prefix that fits the limit still answers.
	res, err = suite.baseApp.Query(context.TODO(), &abci.RequestQuery{Path: "/store/key1/subspace", Data: []byte("p/000")})
	require.NoError(t, err)
	require.Equal(t, uint32(0), res.Code, res.Log)
	require.NotEmpty(t, res.Value)
}
