package keeper_test

import (
	"context"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/distribution"
	"github.com/cosmos/cosmos-sdk/x/distribution/keeper"
	distrtestutil "github.com/cosmos/cosmos-sdk/x/distribution/testutil"
	disttypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// The app wires *stakingkeeper.Keeper, which must serve ExportGenesis.
var _ interface {
	GetHistoricalInfo(context.Context, int64) (stakingtypes.HistoricalInfo, error)
} = (*stakingkeeper.Keeper)(nil)

// stakingWithHistory adds staking's GetHistoricalInfo to the mock keeper.
type stakingWithHistory struct {
	*distrtestutil.MockStakingKeeper
	hist map[int64]stakingtypes.HistoricalInfo
}

func (s stakingWithHistory) GetHistoricalInfo(_ context.Context, height int64) (stakingtypes.HistoricalInfo, error) {
	hi, ok := s.hist[height]
	if !ok {
		return stakingtypes.HistoricalInfo{}, stakingtypes.ErrNoHistoricalInfo
	}
	return hi, nil
}

// The proposer of the last block is exported although BeginBlocker no
// longer stores it.
func TestExportGenesisPreviousProposer(t *testing.T) {
	ctrl := gomock.NewController(t)
	key := storetypes.NewKVStoreKey(disttypes.StoreKey)
	testCtx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))
	encCfg := moduletestutil.MakeTestEncodingConfig(distribution.AppModuleBasic{})

	accountKeeper := distrtestutil.NewMockAccountKeeper(ctrl)
	accountKeeper.EXPECT().GetModuleAddress(disttypes.ModuleName).Return(distrAcc.GetAddress()).AnyTimes()
	accountKeeper.EXPECT().GetModuleAccount(gomock.Any(), gomock.Any()).Return(distrAcc).AnyTimes()
	accountKeeper.EXPECT().AddressCodec().Return(address.NewBech32Codec("cosmos")).AnyTimes()
	accountKeeper.EXPECT().SetModuleAccount(gomock.Any(), gomock.Any()).AnyTimes()
	bankKeeper := distrtestutil.NewMockBankKeeper(ctrl)
	bankKeeper.EXPECT().GetAllBalances(gomock.Any(), gomock.Any()).Return(sdk.Coins{}).AnyTimes()
	mock := distrtestutil.NewMockStakingKeeper(ctrl)
	mock.EXPECT().ValidatorAddressCodec().Return(address.NewBech32Codec("cosmosvaloper")).AnyTimes()
	mock.EXPECT().ConsensusAddressCodec().Return(address.NewBech32Codec("cosmosvalcons")).AnyTimes()

	genesisProposer := sdk.ConsAddress("genesis-proposer")
	lastProposer := sdk.ConsAddress("last-proposer")
	sk := stakingWithHistory{MockStakingKeeper: mock, hist: map[int64]stakingtypes.HistoricalInfo{
		10: {Header: cmtproto.Header{Height: 10, ProposerAddress: lastProposer}},
	}}

	k := keeper.NewKeeper(encCfg.Codec, runtime.NewKVStoreService(key), accountKeeper, bankKeeper, sk,
		"fee_collector", authtypes.NewModuleAddress("gov").String())

	gs := disttypes.DefaultGenesisState()
	gs.PreviousProposer = genesisProposer.String()
	k.InitGenesis(testCtx.Ctx, *gs)

	ctx := testCtx.Ctx.WithBlockHeight(10).WithBlockHeader(cmtproto.Header{Height: 10, ProposerAddress: lastProposer})
	require.NoError(t, k.BeginBlocker(ctx))

	require.Equal(t, lastProposer.String(), k.ExportGenesis(ctx).PreviousProposer)
	// no historical info for the height: the stored key
	require.Equal(t, genesisProposer.String(), k.ExportGenesis(ctx.WithBlockHeight(11)).PreviousProposer)
}
