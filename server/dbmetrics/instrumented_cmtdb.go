package dbmetrics

import (
	cmtdb "github.com/cometbft/cometbft-db"
)

var _ cmtdb.DB = (*InstrumentedCmtDB)(nil)

// InstrumentedCmtDB wraps a cometbft-db.DB and records metrics.
// This is needed because CometBFT uses cometbft-db.DB (not cosmos-db.DB).
type InstrumentedCmtDB struct {
	inner   cmtdb.DB
	metrics *DBMetrics
}

func NewInstrumentedCmtDB(db cmtdb.DB, metrics *DBMetrics) *InstrumentedCmtDB {
	return &InstrumentedCmtDB{inner: db, metrics: metrics}
}

func (db *InstrumentedCmtDB) Inner() cmtdb.DB      { return db.inner }
func (db *InstrumentedCmtDB) Metrics() *DBMetrics { return db.metrics }

func (db *InstrumentedCmtDB) Get(key []byte) ([]byte, error) {
	val, err := db.inner.Get(key)
	n := len(key)
	if val != nil {
		n += len(val)
	}
	db.metrics.Get.Record(n)
	return val, err
}

func (db *InstrumentedCmtDB) Has(key []byte) (bool, error) {
	ok, err := db.inner.Has(key)
	db.metrics.Get.Record(len(key))
	return ok, err
}

func (db *InstrumentedCmtDB) Set(key, value []byte) error {
	err := db.inner.Set(key, value)
	db.metrics.Set.Record(len(key) + len(value))
	return err
}

func (db *InstrumentedCmtDB) SetSync(key, value []byte) error {
	err := db.inner.SetSync(key, value)
	db.metrics.Set.Record(len(key) + len(value))
	return err
}

func (db *InstrumentedCmtDB) Delete(key []byte) error {
	err := db.inner.Delete(key)
	db.metrics.Delete.Record(len(key))
	db.metrics.PruneDel.Record(len(key))
	return err
}

func (db *InstrumentedCmtDB) DeleteSync(key []byte) error {
	err := db.inner.DeleteSync(key)
	db.metrics.Delete.Record(len(key))
	db.metrics.PruneDel.Record(len(key))
	return err
}

func (db *InstrumentedCmtDB) NewBatch() cmtdb.Batch {
	return &InstrumentedCmtBatch{
		inner:   db.inner.NewBatch(),
		metrics: db.metrics,
	}
}

func (db *InstrumentedCmtDB) Iterator(start, end []byte) (cmtdb.Iterator, error) {
	return db.inner.Iterator(start, end)
}

func (db *InstrumentedCmtDB) ReverseIterator(start, end []byte) (cmtdb.Iterator, error) {
	return db.inner.ReverseIterator(start, end)
}

func (db *InstrumentedCmtDB) Close() error   { return db.inner.Close() }
func (db *InstrumentedCmtDB) Print() error   { return db.inner.Print() }
func (db *InstrumentedCmtDB) Stats() map[string]string { return db.inner.Stats() }
func (db *InstrumentedCmtDB) Compact(start, end []byte) error { return db.inner.Compact(start, end) }

// InstrumentedCmtBatch wraps a cometbft-db.Batch with metrics.
type InstrumentedCmtBatch struct {
	inner       cmtdb.Batch
	metrics     *DBMetrics
	setOps      uint64
	setBytes    uint64
	deleteOps   uint64
	deleteBytes uint64
}

func (b *InstrumentedCmtBatch) Set(key, value []byte) error {
	err := b.inner.Set(key, value)
	b.setOps++
	b.setBytes += uint64(len(key) + len(value))
	return err
}

func (b *InstrumentedCmtBatch) Delete(key []byte) error {
	err := b.inner.Delete(key)
	b.deleteOps++
	b.deleteBytes += uint64(len(key))
	return err
}

func (b *InstrumentedCmtBatch) Write() error {
	err := b.inner.Write()
	b.flush()
	return err
}

func (b *InstrumentedCmtBatch) WriteSync() error {
	err := b.inner.WriteSync()
	b.flush()
	return err
}

func (b *InstrumentedCmtBatch) Close() error {
	return b.inner.Close()
}

func (b *InstrumentedCmtBatch) flush() {
	b.metrics.Set.Ops.Add(b.setOps)
	b.metrics.Set.Bytes.Add(b.setBytes)
	b.metrics.Delete.Ops.Add(b.deleteOps)
	b.metrics.Delete.Bytes.Add(b.deleteBytes)
	b.metrics.PruneDel.Ops.Add(b.deleteOps)
	b.metrics.PruneDel.Bytes.Add(b.deleteBytes)
	b.metrics.BatchWrite.Ops.Add(1)
}

// WrapCmtDB creates an InstrumentedCmtDB and returns it along with its metrics.
func WrapCmtDB(db cmtdb.DB, name string) (*InstrumentedCmtDB, *DBMetrics) {
	m := NewDBMetrics(name)
	idb := NewInstrumentedCmtDB(db, m)
	return idb, m
}
