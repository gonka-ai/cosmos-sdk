package dbmetrics

import (
	dbm "github.com/cosmos/cosmos-db"
)

var _ dbm.DB = (*InstrumentedDB)(nil)

// InstrumentedDB wraps a dbm.DB and records metrics for every operation.
type InstrumentedDB struct {
	inner   dbm.DB
	metrics *DBMetrics
}

func NewInstrumentedDB(db dbm.DB, metrics *DBMetrics) *InstrumentedDB {
	return &InstrumentedDB{inner: db, metrics: metrics}
}

func (db *InstrumentedDB) Inner() dbm.DB { return db.inner }
func (db *InstrumentedDB) Metrics() *DBMetrics { return db.metrics }

func (db *InstrumentedDB) Get(key []byte) ([]byte, error) {
	val, err := db.inner.Get(key)
	n := len(key)
	if val != nil {
		n += len(val)
	}
	db.metrics.Get.Record(n)
	if mod := extractModule(key); mod != "" {
		db.metrics.Modules.GetOrCreate(mod).Get.Record(n)
	}
	return val, err
}

func (db *InstrumentedDB) Has(key []byte) (bool, error) {
	ok, err := db.inner.Has(key)
	db.metrics.Get.Record(len(key))
	return ok, err
}

func (db *InstrumentedDB) Set(key, value []byte) error {
	err := db.inner.Set(key, value)
	n := len(key) + len(value)
	db.metrics.Set.Record(n)
	if mod := extractModule(key); mod != "" {
		db.metrics.Modules.GetOrCreate(mod).Set.Record(n)
	}
	return err
}

func (db *InstrumentedDB) SetSync(key, value []byte) error {
	err := db.inner.SetSync(key, value)
	n := len(key) + len(value)
	db.metrics.Set.Record(n)
	if mod := extractModule(key); mod != "" {
		db.metrics.Modules.GetOrCreate(mod).Set.Record(n)
	}
	return err
}

func (db *InstrumentedDB) Delete(key []byte) error {
	err := db.inner.Delete(key)
	n := len(key)
	db.metrics.Delete.Record(n)
	db.classifyAndRecordDelete(key, n)
	return err
}

func (db *InstrumentedDB) DeleteSync(key []byte) error {
	err := db.inner.DeleteSync(key)
	n := len(key)
	db.metrics.Delete.Record(n)
	db.classifyAndRecordDelete(key, n)
	return err
}

func (db *InstrumentedDB) classifyAndRecordDelete(key []byte, n int) {
	class := classifyDelete(key)
	switch class {
	case "prune":
		db.metrics.PruneDel.Record(n)
	case "state":
		db.metrics.StateDel.Record(n)
	}
	if mod := extractModule(key); mod != "" {
		ops := db.metrics.Modules.GetOrCreate(mod)
		ops.Delete.Record(n)
		if class == "prune" {
			ops.PruneDel.Record(n)
		}
	}
}

func (db *InstrumentedDB) NewBatch() dbm.Batch {
	return &InstrumentedBatch{
		inner:   db.inner.NewBatch(),
		metrics: db.metrics,
	}
}

func (db *InstrumentedDB) NewBatchWithSize(size int) dbm.Batch {
	return &InstrumentedBatch{
		inner:   db.inner.NewBatchWithSize(size),
		metrics: db.metrics,
	}
}

func (db *InstrumentedDB) Iterator(start, end []byte) (dbm.Iterator, error) {
	return db.inner.Iterator(start, end)
}

func (db *InstrumentedDB) ReverseIterator(start, end []byte) (dbm.Iterator, error) {
	return db.inner.ReverseIterator(start, end)
}

func (db *InstrumentedDB) Close() error {
	return db.inner.Close()
}

func (db *InstrumentedDB) Print() error {
	return db.inner.Print()
}

func (db *InstrumentedDB) Stats() map[string]string {
	return db.inner.Stats()
}

func (db *InstrumentedDB) ForceCompact(start, limit []byte) error {
	type compactor interface {
		ForceCompact(start, limit []byte) error
	}
	if c, ok := db.inner.(compactor); ok {
		return c.ForceCompact(start, limit)
	}
	return nil
}
