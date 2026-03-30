package dbmetrics

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-metrics"

	dbm "github.com/cosmos/cosmos-db"
)

// Poller periodically reads LevelDB stats and DB wrapper counters,
// then emits them via the hashicorp/go-metrics sink (Cosmos telemetry).
type Poller struct {
	dbs      []*InstrumentedDB
	interval time.Duration
}

func NewPoller(interval time.Duration, dbs ...*InstrumentedDB) *Poller {
	return &Poller{dbs: dbs, interval: interval}
}

// Run starts the polling loop. It blocks until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, db := range p.dbs {
				p.emitDBCounters(db)
				p.emitLevelDBStats(db)
			}
		}
	}
}

func (p *Poller) emitDBCounters(db *InstrumentedDB) {
	m := db.Metrics()
	label := metrics.Label{Name: "db", Value: m.Name}

	setGaugeWithLabel("db_get_ops_total", float32(m.Get.Ops.Load()), label)
	setGaugeWithLabel("db_get_bytes_total", float32(m.Get.Bytes.Load()), label)
	setGaugeWithLabel("db_set_ops_total", float32(m.Set.Ops.Load()), label)
	setGaugeWithLabel("db_set_bytes_total", float32(m.Set.Bytes.Load()), label)
	setGaugeWithLabel("db_delete_ops_total", float32(m.Delete.Ops.Load()), label)
	setGaugeWithLabel("db_delete_bytes_total", float32(m.Delete.Bytes.Load()), label)
	setGaugeWithLabel("db_batch_write_ops_total", float32(m.BatchWrite.Ops.Load()), label)
	setGaugeWithLabel("db_batch_write_bytes_total", float32(m.BatchWrite.Bytes.Load()), label)
	setGaugeWithLabel("db_prune_delete_ops_total", float32(m.PruneDel.Ops.Load()), label)
	setGaugeWithLabel("db_prune_delete_bytes_total", float32(m.PruneDel.Bytes.Load()), label)
	setGaugeWithLabel("db_state_delete_ops_total", float32(m.StateDel.Ops.Load()), label)
	setGaugeWithLabel("db_state_delete_bytes_total", float32(m.StateDel.Bytes.Load()), label)

	m.Modules.ForEach(func(module string, ops *ModuleOps) {
		modLabel := metrics.Label{Name: "module", Value: module}
		setGaugeWithLabels("db_module_get_ops_total", float32(ops.Get.Ops.Load()), label, modLabel)
		setGaugeWithLabels("db_module_get_bytes_total", float32(ops.Get.Bytes.Load()), label, modLabel)
		setGaugeWithLabels("db_module_set_ops_total", float32(ops.Set.Ops.Load()), label, modLabel)
		setGaugeWithLabels("db_module_set_bytes_total", float32(ops.Set.Bytes.Load()), label, modLabel)
		setGaugeWithLabels("db_module_delete_ops_total", float32(ops.Delete.Ops.Load()), label, modLabel)
		setGaugeWithLabels("db_module_delete_bytes_total", float32(ops.Delete.Bytes.Load()), label, modLabel)
		setGaugeWithLabels("db_module_prune_delete_ops_total", float32(ops.PruneDel.Ops.Load()), label, modLabel)
		setGaugeWithLabels("db_module_prune_delete_bytes_total", float32(ops.PruneDel.Bytes.Load()), label, modLabel)
	})
}

func (p *Poller) emitLevelDBStats(db *InstrumentedDB) {
	stats := PollLevelDBStats(db.Inner())
	if stats == nil {
		return
	}

	label := metrics.Label{Name: "db", Value: db.Metrics().Name}

	setGaugeWithLabel("leveldb_comp_mem_count", float32(stats.CompMemCount), label)
	setGaugeWithLabel("leveldb_comp_level0_count", float32(stats.CompLevel0Count), label)
	setGaugeWithLabel("leveldb_comp_nonlevel0_count", float32(stats.CompNonLevel0Count), label)
	setGaugeWithLabel("leveldb_comp_seek_count", float32(stats.CompSeekCount), label)
	setGaugeWithLabel("leveldb_io_read_bytes", float32(stats.IOReadBytes), label)
	setGaugeWithLabel("leveldb_io_write_bytes", float32(stats.IOWriteBytes), label)
	setGaugeWithLabel("leveldb_write_delay_count", float32(stats.WriteDelayCount), label)
	setGaugeWithLabel("leveldb_write_delay_seconds", float32(stats.WriteDelayDuration.Seconds()), label)
	paused := float32(0)
	if stats.WritePaused {
		paused = 1
	}
	setGaugeWithLabel("leveldb_write_paused", paused, label)
	setGaugeWithLabel("leveldb_alive_iterators", float32(stats.AliveIterators), label)
	setGaugeWithLabel("leveldb_alive_snapshots", float32(stats.AliveSnapshots), label)
	setGaugeWithLabel("leveldb_cached_block_size", float32(stats.CachedBlockSize), label)

	for level, tables := range stats.LevelTables {
		lvlLabel := metrics.Label{Name: "level", Value: fmt.Sprintf("%d", level)}
		setGaugeWithLabels("leveldb_level_tables", float32(tables), label, lvlLabel)
	}
	for level, sizeMB := range stats.LevelSizes {
		lvlLabel := metrics.Label{Name: "level", Value: fmt.Sprintf("%d", level)}
		setGaugeWithLabels("leveldb_level_size_bytes", float32(sizeMB*1048576), label, lvlLabel)
	}
}

func setGaugeWithLabel(key string, val float32, label metrics.Label) {
	metrics.SetGaugeWithLabels([]string{key}, val, []metrics.Label{label})
}

func setGaugeWithLabels(key string, val float32, labels ...metrics.Label) {
	metrics.SetGaugeWithLabels([]string{key}, val, labels)
}

// WrapDB creates an InstrumentedDB and returns both the wrapped DB and the
// raw InstrumentedDB (for later poller registration).
func WrapDB(db dbm.DB, name string) (*InstrumentedDB, *DBMetrics) {
	m := NewDBMetrics(name)
	idb := NewInstrumentedDB(db, m)
	return idb, m
}

// CmtPoller periodically reads DB wrapper counters for CometBFT DBs
// and emits them via the hashicorp/go-metrics sink.
type CmtPoller struct {
	dbs      []*InstrumentedCmtDB
	interval time.Duration
}

func NewCmtPoller(interval time.Duration, dbs ...*InstrumentedCmtDB) *CmtPoller {
	return &CmtPoller{dbs: dbs, interval: interval}
}

func (p *CmtPoller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, db := range p.dbs {
				p.emitCmtDBCounters(db)
			}
		}
	}
}

func (p *CmtPoller) emitCmtDBCounters(db *InstrumentedCmtDB) {
	m := db.Metrics()
	label := metrics.Label{Name: "db", Value: m.Name}

	setGaugeWithLabel("db_get_ops_total", float32(m.Get.Ops.Load()), label)
	setGaugeWithLabel("db_get_bytes_total", float32(m.Get.Bytes.Load()), label)
	setGaugeWithLabel("db_set_ops_total", float32(m.Set.Ops.Load()), label)
	setGaugeWithLabel("db_set_bytes_total", float32(m.Set.Bytes.Load()), label)
	setGaugeWithLabel("db_delete_ops_total", float32(m.Delete.Ops.Load()), label)
	setGaugeWithLabel("db_delete_bytes_total", float32(m.Delete.Bytes.Load()), label)
	setGaugeWithLabel("db_batch_write_ops_total", float32(m.BatchWrite.Ops.Load()), label)
	setGaugeWithLabel("db_batch_write_bytes_total", float32(m.BatchWrite.Bytes.Load()), label)
	setGaugeWithLabel("db_prune_delete_ops_total", float32(m.PruneDel.Ops.Load()), label)
	setGaugeWithLabel("db_prune_delete_bytes_total", float32(m.PruneDel.Bytes.Load()), label)
}
