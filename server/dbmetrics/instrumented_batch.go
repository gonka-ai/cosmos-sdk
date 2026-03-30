package dbmetrics

import (
	"sync/atomic"

	dbm "github.com/cosmos/cosmos-db"
)

var _ dbm.Batch = (*InstrumentedBatch)(nil)

// InstrumentedBatch wraps a dbm.Batch and records metrics.
// Batch operations accumulate locally and are attributed on Write/WriteSync.
type InstrumentedBatch struct {
	inner   dbm.Batch
	metrics *DBMetrics

	setOps       atomic.Uint64
	setBytes     atomic.Uint64
	deleteOps    atomic.Uint64
	deleteBytes  atomic.Uint64
	pruneDelOps  atomic.Uint64
	pruneDelBytes atomic.Uint64
	stateDelOps  atomic.Uint64
	stateDelBytes atomic.Uint64

	moduleSet      map[string]CounterSnapshot
	moduleDel      map[string]CounterSnapshot
	modulePruneDel map[string]CounterSnapshot
}

func (b *InstrumentedBatch) Set(key, value []byte) error {
	err := b.inner.Set(key, value)
	n := len(key) + len(value)
	b.setOps.Add(1)
	b.setBytes.Add(uint64(n))
	if mod := extractModule(key); mod != "" {
		if b.moduleSet == nil {
			b.moduleSet = make(map[string]CounterSnapshot)
		}
		s := b.moduleSet[mod]
		s.Ops++
		s.Bytes += uint64(n)
		b.moduleSet[mod] = s
	}
	return err
}

func (b *InstrumentedBatch) Delete(key []byte) error {
	err := b.inner.Delete(key)
	n := len(key)
	b.deleteOps.Add(1)
	b.deleteBytes.Add(uint64(n))

	class := classifyDelete(key)
	switch class {
	case "prune":
		b.pruneDelOps.Add(1)
		b.pruneDelBytes.Add(uint64(n))
	case "state":
		b.stateDelOps.Add(1)
		b.stateDelBytes.Add(uint64(n))
	}

	mod := extractModule(key)
	if mod != "" {
		if b.moduleDel == nil {
			b.moduleDel = make(map[string]CounterSnapshot)
		}
		s := b.moduleDel[mod]
		s.Ops++
		s.Bytes += uint64(n)
		b.moduleDel[mod] = s

		if class == "prune" {
			if b.modulePruneDel == nil {
				b.modulePruneDel = make(map[string]CounterSnapshot)
			}
			p := b.modulePruneDel[mod]
			p.Ops++
			p.Bytes += uint64(n)
			b.modulePruneDel[mod] = p
		}
	}
	return err
}

func (b *InstrumentedBatch) Write() error {
	batchSize := b.captureBatchSize()
	err := b.inner.Write()
	b.flush(batchSize)
	return err
}

func (b *InstrumentedBatch) WriteSync() error {
	batchSize := b.captureBatchSize()
	err := b.inner.WriteSync()
	b.flush(batchSize)
	return err
}

func (b *InstrumentedBatch) captureBatchSize() uint64 {
	if size, err := b.inner.GetByteSize(); err == nil && size > 0 {
		return uint64(size)
	}
	return b.setBytes.Load() + b.deleteBytes.Load()
}

func (b *InstrumentedBatch) flush(batchSize uint64) {
	b.metrics.Set.Ops.Add(b.setOps.Load())
	b.metrics.Set.Bytes.Add(b.setBytes.Load())
	b.metrics.Delete.Ops.Add(b.deleteOps.Load())
	b.metrics.Delete.Bytes.Add(b.deleteBytes.Load())
	b.metrics.PruneDel.Ops.Add(b.pruneDelOps.Load())
	b.metrics.PruneDel.Bytes.Add(b.pruneDelBytes.Load())
	b.metrics.StateDel.Ops.Add(b.stateDelOps.Load())
	b.metrics.StateDel.Bytes.Add(b.stateDelBytes.Load())
	b.metrics.BatchWrite.Ops.Add(1)
	b.metrics.BatchWrite.Bytes.Add(batchSize)

	for mod, snap := range b.moduleSet {
		ops := b.metrics.Modules.GetOrCreate(mod)
		ops.Set.Ops.Add(snap.Ops)
		ops.Set.Bytes.Add(snap.Bytes)
	}
	for mod, snap := range b.moduleDel {
		ops := b.metrics.Modules.GetOrCreate(mod)
		ops.Delete.Ops.Add(snap.Ops)
		ops.Delete.Bytes.Add(snap.Bytes)
	}
	for mod, snap := range b.modulePruneDel {
		ops := b.metrics.Modules.GetOrCreate(mod)
		ops.PruneDel.Ops.Add(snap.Ops)
		ops.PruneDel.Bytes.Add(snap.Bytes)
	}
}

func (b *InstrumentedBatch) Close() error {
	return b.inner.Close()
}

func (b *InstrumentedBatch) GetByteSize() (int, error) {
	return b.inner.GetByteSize()
}
