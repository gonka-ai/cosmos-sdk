package snapshots

import (
	"bytes"
	"io"
	"testing"
	"time"

	db "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"
)

func TestStore_PruneDeletesSnapshotAfterLeaseExpires(t *testing.T) {
	s, err := NewStore(db.NewMemDB(), t.TempDir())
	require.NoError(t, err)
	for height := uint64(1); height <= 2; height++ {
		ch := make(chan io.ReadCloser, 2)
		for i := byte(0); i < 2; i++ {
			ch <- io.NopCloser(bytes.NewReader([]byte{byte(height), i}))
		}
		close(ch)
		_, err = s.Save(height, 1, ch)
		require.NoError(t, err)
	}

	r, err := s.LoadChunk(1, 1, 0)
	require.NoError(t, err)
	require.NotNil(t, r)
	require.NoError(t, r.Close())

	later := time.Now().Add(DefaultServedLease + time.Second)
	s.now = func() time.Time { return later }

	_, err = s.Prune(1)
	require.NoError(t, err)

	snapshot, err := s.Get(1, 1)
	require.NoError(t, err)
	require.Nil(t, snapshot)
}
