package pruning_test

import (
	"errors"
	"fmt"
	"testing"

	db "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"cosmossdk.io/log"
	"cosmossdk.io/store/mock"
	"cosmossdk.io/store/pruning"
	"cosmossdk.io/store/pruning/types"
)

const dbErr = "db error"

func TestNewManager(t *testing.T) {
	manager := pruning.NewManager(db.NewMemDB(), log.NewNopLogger())
	require.NotNil(t, manager)
	require.Equal(t, types.PruningNothing, manager.GetOptions().GetPruningStrategy())
}

func TestStrategies(t *testing.T) {
	testcases := map[string]struct {
		strategy         types.PruningOptions
		snapshotInterval uint64
		strategyToAssert types.PruningStrategy
		isValid          bool
	}{
		"prune nothing - no snapshot": {
			strategy:         types.NewPruningOptions(types.PruningNothing),
			strategyToAssert: types.PruningNothing,
		},
		"prune nothing - snapshot": {
			strategy:         types.NewPruningOptions(types.PruningNothing),
			strategyToAssert: types.PruningNothing,
			snapshotInterval: 100,
		},
		"prune default - no snapshot": {
			strategy:         types.NewPruningOptions(types.PruningDefault),
			strategyToAssert: types.PruningDefault,
		},
		"prune default - snapshot": {
			strategy:         types.NewPruningOptions(types.PruningDefault),
			strategyToAssert: types.PruningDefault,
			snapshotInterval: 100,
		},
		"prune everything - no snapshot": {
			strategy:         types.NewPruningOptions(types.PruningEverything),
			strategyToAssert: types.PruningEverything,
		},
		"prune everything - snapshot": {
			strategy:         types.NewPruningOptions(types.PruningEverything),
			strategyToAssert: types.PruningEverything,
			snapshotInterval: 100,
		},
		"custom 100-10-15": {
			strategy:         types.NewCustomPruningOptions(100, 15),
			snapshotInterval: 10,
			strategyToAssert: types.PruningCustom,
		},
		"custom 10-10-15": {
			strategy:         types.NewCustomPruningOptions(10, 15),
			snapshotInterval: 10,
			strategyToAssert: types.PruningCustom,
		},
		"custom 100-0-15": {
			strategy:         types.NewCustomPruningOptions(100, 15),
			snapshotInterval: 0,
			strategyToAssert: types.PruningCustom,
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			manager := pruning.NewManager(db.NewMemDB(), log.NewNopLogger())
			require.NotNil(t, manager)

			curStrategy := tc.strategy
			manager.SetSnapshotInterval(tc.snapshotInterval)

			pruneStrategy := curStrategy.GetPruningStrategy()
			require.Equal(t, tc.strategyToAssert, pruneStrategy)

			// Validate strategy parameters
			switch pruneStrategy {
			case types.PruningDefault:
				require.Equal(t, uint64(362880), curStrategy.KeepRecent)
				require.Equal(t, uint64(10), curStrategy.Interval)
			case types.PruningNothing:
				require.Equal(t, uint64(0), curStrategy.KeepRecent)
				require.Equal(t, uint64(0), curStrategy.Interval)
			case types.PruningEverything:
				require.Equal(t, uint64(2), curStrategy.KeepRecent)
				require.Equal(t, uint64(10), curStrategy.Interval)
			default:
				//
			}

			manager.SetOptions(curStrategy)
			require.Equal(t, tc.strategy, manager.GetOptions())

			curKeepRecent := curStrategy.KeepRecent
			snHeight := int64(tc.snapshotInterval - 1)
			for curHeight := int64(0); curHeight < 110000; curHeight++ {
				if tc.snapshotInterval != 0 {
					if curHeight > int64(tc.snapshotInterval) && curHeight%int64(tc.snapshotInterval) == int64(tc.snapshotInterval)-1 {
						manager.HandleSnapshotHeight(curHeight - int64(tc.snapshotInterval) + 1)
						snHeight = curHeight
					}
				}

				pruningHeightActual := manager.GetPruningHeight(curHeight)
				curHeightStr := fmt.Sprintf("height: %d", curHeight)

				switch curStrategy.GetPruningStrategy() {
				case types.PruningNothing:
					require.Equal(t, int64(0), pruningHeightActual, curHeightStr)
				default:
					if curHeight > int64(curKeepRecent) && curHeight%int64(curStrategy.Interval) == 0 {
						pruningHeightExpected := curHeight - int64(curKeepRecent) - 1
						if tc.snapshotInterval > 0 && snHeight < pruningHeightExpected {
							pruningHeightExpected = snHeight
						}
						require.Equal(t, pruningHeightExpected, pruningHeightActual, curHeightStr)
					} else {
						require.Equal(t, int64(0), pruningHeightActual, curHeightStr)
					}
				}
			}
		})
	}
}

func TestPruningHeight_Inputs(t *testing.T) {
	keepRecent := int64(types.NewPruningOptions(types.PruningEverything).KeepRecent)
	interval := int64(types.NewPruningOptions(types.PruningEverything).Interval)

	testcases := map[string]struct {
		height         int64
		expectedResult int64
		strategy       types.PruningStrategy
	}{
		"currentHeight is negative - prune everything - invalid currentHeight": {
			-1,
			0,
			types.PruningEverything,
		},
		"currentHeight is  zero - prune everything - invalid currentHeight": {
			0,
			0,
			types.PruningEverything,
		},
		"currentHeight is positive but within keep recent- prune everything - not kept": {
			keepRecent,
			0,
			types.PruningEverything,
		},
		"currentHeight is positive and equal to keep recent+1 - no kept": {
			keepRecent + 1,
			0,
			types.PruningEverything,
		},
		"currentHeight is positive and greater than keep recent+1 but not multiple of interval - no kept": {
			keepRecent + 2,
			0,
			types.PruningEverything,
		},
		"currentHeight is positive and greater than keep recent+1 and multiple of interval - kept": {
			interval,
			interval - keepRecent - 1,
			types.PruningEverything,
		},
		"pruning nothing, currentHeight is positive and greater than keep recent - not kept": {
			interval,
			0,
			types.PruningNothing,
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			manager := pruning.NewManager(db.NewMemDB(), log.NewNopLogger())
			require.NotNil(t, manager)
			manager.SetOptions(types.NewPruningOptions(tc.strategy))

			pruningHeightActual := manager.GetPruningHeight(tc.height)
			require.Equal(t, tc.expectedResult, pruningHeightActual)
		})
	}
}

func TestHandleSnapshotHeight_DbErr_Panic(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Setup
	dbMock := mock.NewMockDB(ctrl)

	dbMock.EXPECT().SetSync(gomock.Any(), gomock.Any()).Return(errors.New(dbErr)).Times(1)

	manager := pruning.NewManager(dbMock, log.NewNopLogger())
	manager.SetOptions(types.NewPruningOptions(types.PruningEverything))
	require.NotNil(t, manager)

	defer func() {
		if r := recover(); r == nil {
			t.Fail()
		}
	}()

	manager.HandleSnapshotHeight(10)
}

func TestHandleSnapshotHeight_LoadFromDisk(t *testing.T) {
	snapshotInterval := uint64(10)

	// Setup
	db := db.NewMemDB()
	manager := pruning.NewManager(db, log.NewNopLogger())
	require.NotNil(t, manager)

	manager.SetOptions(types.NewPruningOptions(types.PruningEverything))
	manager.SetSnapshotInterval(snapshotInterval)

	expected := 0
	for snapshotHeight := int64(-1); snapshotHeight < 100; snapshotHeight++ {
		snapshotHeightStr := fmt.Sprintf("snaphost height: %d", snapshotHeight)
		if snapshotHeight > int64(snapshotInterval) && snapshotHeight%int64(snapshotInterval) == 1 {
			// Test flush
			manager.HandleSnapshotHeight(snapshotHeight - 1)
			expected = 1
		}

		loadedSnapshotHeights, err := pruning.LoadPruningSnapshotHeights(db)
		require.NoError(t, err)
		require.Equal(t, expected, len(loadedSnapshotHeights), snapshotHeightStr)

		// Test load back
		err = manager.LoadSnapshotHeights(db)
		require.NoError(t, err)

		loadedSnapshotHeights, err = pruning.LoadPruningSnapshotHeights(db)
		require.NoError(t, err)
		require.Equal(t, expected, len(loadedSnapshotHeights), snapshotHeightStr)
	}
}

func TestLoadPruningSnapshotHeights(t *testing.T) {
	var (
		manager = pruning.NewManager(db.NewMemDB(), log.NewNopLogger())
		err     error
	)
	require.NotNil(t, manager)

	// must not be PruningNothing
	manager.SetOptions(types.NewPruningOptions(types.PruningDefault))

	testcases := map[string]struct {
		getFlushedPruningSnapshotHeights func() []int64
		expectedResult                   error
	}{
		"negative snapshotPruningHeight - error": {
			getFlushedPruningSnapshotHeights: func() []int64 {
				return []int64{5, -2, 3}
			},
			expectedResult: &pruning.NegativeHeightsError{Height: -2},
		},
		"non-negative - success": {
			getFlushedPruningSnapshotHeights: func() []int64 {
				return []int64{5, 0, 3}
			},
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			db := db.NewMemDB()

			if tc.getFlushedPruningSnapshotHeights != nil {
				err = db.Set(pruning.PruneSnapshotHeightsKey, pruning.Int64SliceToBytes(tc.getFlushedPruningSnapshotHeights()))
				require.NoError(t, err)
			}

			err = manager.LoadSnapshotHeights(db)
			require.Equal(t, tc.expectedResult, err)
		})
	}
}

func TestLoadSnapshotHeights_PruneNothing(t *testing.T) {
	manager := pruning.NewManager(db.NewMemDB(), log.NewNopLogger())
	require.NotNil(t, manager)

	manager.SetOptions(types.NewPruningOptions(types.PruningNothing))

	require.Nil(t, manager.LoadSnapshotHeights(db.NewMemDB()))
}

func TestHandleSnapshotHeight_StateSyncNode(t *testing.T) {
	// Regression test: state-synced nodes start at a high height (e.g. 2,896,000).
	// The initial 0 in pruneSnapshotHeights can never form a contiguous chain with
	// real snapshot heights, causing GetPruningHeight to return 0 + snapshotInterval - 1
	// which caps pruning at a height that doesn't exist. IAVL versions accumulate forever.

	snapshotInterval := uint64(1000)
	keepRecent := uint64(1000)
	interval := uint64(100)
	stateSyncHeight := int64(2_896_000)

	manager := pruning.NewManager(db.NewMemDB(), log.NewNopLogger())
	require.NotNil(t, manager)

	manager.SetOptions(types.NewCustomPruningOptions(keepRecent, interval))
	manager.SetSnapshotInterval(snapshotInterval)

	// Simulate first snapshot after state-sync (at stateSyncHeight + snapshotInterval)
	firstSnapshotHeight := stateSyncHeight + int64(snapshotInterval)
	manager.HandleSnapshotHeight(firstSnapshotHeight)

	// At height well past keep-recent, pruning should return a meaningful height
	currentHeight := firstSnapshotHeight + int64(keepRecent) + int64(interval)
	pruneHeight := manager.GetPruningHeight(currentHeight)

	// Without the fix, pruneHeight would be 999 (0 + 1000 - 1), which is useless
	// for a node that started at height 2,896,000.
	// With the fix, pruneHeight should be close to currentHeight - keepRecent - 1.
	// The key assertion: pruneHeight must be > snapshotInterval (the broken cap from stale 0).
	require.Greater(t, pruneHeight, int64(snapshotInterval),
		"state-synced node should prune beyond the stale 0 cap")

	// Verify the actual value is sensible (around firstSnapshotHeight)
	expectedPruneHeight := currentHeight - 1 - int64(keepRecent) // 2_897_099
	require.Equal(t, expectedPruneHeight, pruneHeight,
		"prune height should be currentHeight - 1 - keepRecent")
}

func TestHandleSnapshotHeight_GenesisNode(t *testing.T) {
	// Verify that genesis nodes (starting from height 0) still work correctly.
	// The initial 0 should NOT be removed when snapshots form a contiguous chain.

	snapshotInterval := uint64(1000)
	keepRecent := uint64(1000)
	interval := uint64(100)

	manager := pruning.NewManager(db.NewMemDB(), log.NewNopLogger())
	require.NotNil(t, manager)

	manager.SetOptions(types.NewCustomPruningOptions(keepRecent, interval))
	manager.SetSnapshotInterval(snapshotInterval)

	// Genesis node: snapshots at 1000, 2000, 3000, ...
	for h := int64(snapshotInterval); h <= int64(snapshotInterval)*5; h += int64(snapshotInterval) {
		manager.HandleSnapshotHeight(h)
	}

	// At height 6100 (past keep-recent=1000, multiple of interval=100)
	currentHeight := int64(snapshotInterval)*5 + int64(keepRecent) + int64(interval)
	pruneHeight := manager.GetPruningHeight(currentHeight)

	// Should be able to prune up to some meaningful height
	require.Greater(t, pruneHeight, int64(0), "genesis node pruning should work")
}
