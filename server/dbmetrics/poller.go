package dbmetrics

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-metrics"

	dbm "github.com/cosmos/cosmos-db"
)

// prevCounters tracks previous snapshot for delta computation.
type prevCounters struct {
	Get        CounterSnapshot
	Set        CounterSnapshot
	Delete     CounterSnapshot
	BatchWrite CounterSnapshot
	PruneDel   CounterSnapshot
	StateDel   CounterSnapshot
	Modules    map[string]prevModuleCounters

	// LevelDB cumulative stats from previous poll
	LdbIORead        float64
	LdbIOWrite       float64
	LdbCompMem       uint64
	LdbCompLevel0    uint64
	LdbCompNonLevel0 uint64
	LdbCompSeek      uint64
	LdbWriteDelayN   int64

	// Per-level compaction cumulative stats
	LdbLevelCompTime  map[int]float64 // seconds
	LdbLevelCompRead  map[int]float64 // MB
	LdbLevelCompWrite map[int]float64 // MB
}

type prevModuleCounters struct {
	Get      CounterSnapshot
	Set      CounterSnapshot
	Delete   CounterSnapshot
	PruneDel CounterSnapshot
}

// Poller periodically reads LevelDB stats and DB wrapper counters,
// then emits them via the hashicorp/go-metrics sink (Cosmos telemetry).
type Poller struct {
	getDBs   func() []*InstrumentedDB
	interval time.Duration
	prev     map[string]*prevCounters // keyed by DB name
}

// NewPoller creates a poller that calls getDBs on each tick to discover DBs
// dynamically. This handles DBs registered after the poller is created.
func NewPoller(interval time.Duration, getDBs func() []*InstrumentedDB) *Poller {
	return &Poller{
		getDBs:   getDBs,
		interval: interval,
		prev:     make(map[string]*prevCounters),
	}
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
			for _, db := range p.getDBs() {
				p.emitDBCounters(db)
				p.emitLevelDBStats(db)
			}
		}
	}
}

func (p *Poller) getPrev(name string) *prevCounters {
	pc, ok := p.prev[name]
	if !ok {
		pc = &prevCounters{Modules: make(map[string]prevModuleCounters)}
		p.prev[name] = pc
	}
	return pc
}

func deltaCounter(key string, cur, prev *CounterSnapshot, labels ...metrics.Label) CounterSnapshot {
	dOps := cur.Ops - prev.Ops
	dBytes := cur.Bytes - prev.Bytes
	if dOps > 0 {
		metrics.IncrCounterWithLabels([]string{key + "_ops"}, float32(dOps), labels)
	}
	if dBytes > 0 {
		metrics.IncrCounterWithLabels([]string{key + "_bytes"}, float32(dBytes), labels)
	}
	return *cur
}

func (p *Poller) emitDBCounters(db *InstrumentedDB) {
	m := db.Metrics()
	prev := p.getPrev(m.Name)
	label := metrics.Label{Name: "db", Value: m.Name}
	labels := []metrics.Label{label}

	curGet := m.Get.Snapshot()
	curSet := m.Set.Snapshot()
	curDel := m.Delete.Snapshot()
	curBatch := m.BatchWrite.Snapshot()
	curPrune := m.PruneDel.Snapshot()
	curState := m.StateDel.Snapshot()

	prev.Get = deltaCounter("db_get", &curGet, &prev.Get, labels...)
	prev.Set = deltaCounter("db_set", &curSet, &prev.Set, labels...)
	prev.Delete = deltaCounter("db_delete", &curDel, &prev.Delete, labels...)
	prev.BatchWrite = deltaCounter("db_batch_write", &curBatch, &prev.BatchWrite, labels...)
	prev.PruneDel = deltaCounter("db_prune_delete", &curPrune, &prev.PruneDel, labels...)
	prev.StateDel = deltaCounter("db_state_delete", &curState, &prev.StateDel, labels...)

	m.Modules.ForEach(func(module string, ops *ModuleOps) {
		modLabels := []metrics.Label{label, {Name: "module", Value: module}}
		prevMod := prev.Modules[module]

		curMGet := ops.Get.Snapshot()
		curMSet := ops.Set.Snapshot()
		curMDel := ops.Delete.Snapshot()
		curMPrune := ops.PruneDel.Snapshot()

		prevMod.Get = deltaCounter("db_module_get", &curMGet, &prevMod.Get, modLabels...)
		prevMod.Set = deltaCounter("db_module_set", &curMSet, &prevMod.Set, modLabels...)
		prevMod.Delete = deltaCounter("db_module_delete", &curMDel, &prevMod.Delete, modLabels...)
		prevMod.PruneDel = deltaCounter("db_module_prune_delete", &curMPrune, &prevMod.PruneDel, modLabels...)

		prev.Modules[module] = prevMod
	})
}

func (p *Poller) emitLevelDBStats(db *InstrumentedDB) {
	stats := PollLevelDBStats(db.Inner())
	if stats == nil {
		return
	}
	prev := p.getPrev(db.Metrics().Name)
	emitLevelDBStatsShared(stats, prev, db.Metrics().Name)
}

// emitLevelDBStatsShared emits LevelDB stats for any DB type.
func emitLevelDBStatsShared(stats *LevelDBStats, prev *prevCounters, dbName string) {
	label := metrics.Label{Name: "db", Value: dbName}
	labels := []metrics.Label{label}

	incrFloat := func(key string, cur, prev *float64) {
		d := *cur - *prev
		if d > 0 {
			metrics.IncrCounterWithLabels([]string{key}, float32(d), labels)
		}
		*prev = *cur
	}
	incrUint := func(key string, cur uint64, prev *uint64) {
		d := cur - *prev
		if d > 0 {
			metrics.IncrCounterWithLabels([]string{key}, float32(d), labels)
		}
		*prev = cur
	}
	incrInt := func(key string, cur int64, prev *int64) {
		d := cur - *prev
		if d > 0 {
			metrics.IncrCounterWithLabels([]string{key}, float32(d), labels)
		}
		*prev = cur
	}

	// Cumulative LevelDB values -> counters (deltas)
	incrFloat("leveldb_io_read_bytes", &stats.IOReadBytes, &prev.LdbIORead)
	incrFloat("leveldb_io_write_bytes", &stats.IOWriteBytes, &prev.LdbIOWrite)
	incrUint("leveldb_comp_mem_count", stats.CompMemCount, &prev.LdbCompMem)
	incrUint("leveldb_comp_level0_count", stats.CompLevel0Count, &prev.LdbCompLevel0)
	incrUint("leveldb_comp_nonlevel0_count", stats.CompNonLevel0Count, &prev.LdbCompNonLevel0)
	incrUint("leveldb_comp_seek_count", stats.CompSeekCount, &prev.LdbCompSeek)
	incrInt("leveldb_write_delay_count", stats.WriteDelayCount, &prev.LdbWriteDelayN)

	// Point-in-time LevelDB values -> gauges
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
	if prev.LdbLevelCompTime == nil {
		prev.LdbLevelCompTime = make(map[int]float64)
	}
	if prev.LdbLevelCompRead == nil {
		prev.LdbLevelCompRead = make(map[int]float64)
	}
	if prev.LdbLevelCompWrite == nil {
		prev.LdbLevelCompWrite = make(map[int]float64)
	}
	for level, compTime := range stats.LevelCompTime {
		lvlLabel := metrics.Label{Name: "level", Value: fmt.Sprintf("%d", level)}
		lvlLabels := []metrics.Label{label, lvlLabel}
		d := compTime - prev.LdbLevelCompTime[level]
		if d > 0 {
			metrics.IncrCounterWithLabels([]string{"leveldb_level_comp_time_seconds"}, float32(d), lvlLabels)
		}
		prev.LdbLevelCompTime[level] = compTime
	}
	for level, compReadMB := range stats.LevelCompRead {
		lvlLabel := metrics.Label{Name: "level", Value: fmt.Sprintf("%d", level)}
		lvlLabels := []metrics.Label{label, lvlLabel}
		d := compReadMB - prev.LdbLevelCompRead[level]
		if d > 0 {
			metrics.IncrCounterWithLabels([]string{"leveldb_level_comp_read_bytes"}, float32(d*1048576), lvlLabels)
		}
		prev.LdbLevelCompRead[level] = compReadMB
	}
	for level, compWriteMB := range stats.LevelCompWrite {
		lvlLabel := metrics.Label{Name: "level", Value: fmt.Sprintf("%d", level)}
		lvlLabels := []metrics.Label{label, lvlLabel}
		d := compWriteMB - prev.LdbLevelCompWrite[level]
		if d > 0 {
			metrics.IncrCounterWithLabels([]string{"leveldb_level_comp_write_bytes"}, float32(d*1048576), lvlLabels)
		}
		prev.LdbLevelCompWrite[level] = compWriteMB
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
	getDBs   func() []*InstrumentedCmtDB
	interval time.Duration
	prev     map[string]*prevCounters
}

func NewCmtPoller(interval time.Duration, getDBs func() []*InstrumentedCmtDB) *CmtPoller {
	return &CmtPoller{
		getDBs:   getDBs,
		interval: interval,
		prev:     make(map[string]*prevCounters),
	}
}

func (p *CmtPoller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, db := range p.getDBs() {
				p.emitCmtDBCounters(db)
				p.emitCmtLevelDBStats(db)
			}
		}
	}
}

func (p *CmtPoller) getPrev(name string) *prevCounters {
	pc, ok := p.prev[name]
	if !ok {
		pc = &prevCounters{Modules: make(map[string]prevModuleCounters)}
		p.prev[name] = pc
	}
	return pc
}

func (p *CmtPoller) emitCmtLevelDBStats(db *InstrumentedCmtDB) {
	stats := PollCmtLevelDBStats(db.Inner())
	if stats == nil {
		return
	}
	prev := p.getPrev(db.Metrics().Name)
	emitLevelDBStatsShared(stats, prev, db.Metrics().Name)
}

func (p *CmtPoller) emitCmtDBCounters(db *InstrumentedCmtDB) {
	m := db.Metrics()
	prev := p.getPrev(m.Name)
	label := metrics.Label{Name: "db", Value: m.Name}
	labels := []metrics.Label{label}

	curGet := m.Get.Snapshot()
	curSet := m.Set.Snapshot()
	curDel := m.Delete.Snapshot()
	curBatch := m.BatchWrite.Snapshot()
	curPrune := m.PruneDel.Snapshot()

	prev.Get = deltaCounter("db_get", &curGet, &prev.Get, labels...)
	prev.Set = deltaCounter("db_set", &curSet, &prev.Set, labels...)
	prev.Delete = deltaCounter("db_delete", &curDel, &prev.Delete, labels...)
	prev.BatchWrite = deltaCounter("db_batch_write", &curBatch, &prev.BatchWrite, labels...)
	prev.PruneDel = deltaCounter("db_prune_delete", &curPrune, &prev.PruneDel, labels...)
}
