package dbmetrics

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cmtdb "github.com/cometbft/cometbft-db"
	"github.com/syndtr/goleveldb/leveldb"

	dbm "github.com/cosmos/cosmos-db"
)

// Counters holds atomic counters for a single metric bucket.
type Counters struct {
	Ops   atomic.Uint64
	Bytes atomic.Uint64
}

func (c *Counters) Record(n int) {
	c.Ops.Add(1)
	c.Bytes.Add(uint64(n))
}

// Snapshot returns a point-in-time copy of the counters.
type CounterSnapshot struct {
	Ops   uint64
	Bytes uint64
}

func (c *Counters) Snapshot() CounterSnapshot {
	return CounterSnapshot{Ops: c.Ops.Load(), Bytes: c.Bytes.Load()}
}

// ModuleCounters tracks per-module per-operation counters.
type ModuleCounters struct {
	mu       sync.RWMutex
	modules  map[string]*ModuleOps
}

type ModuleOps struct {
	Get    Counters
	Set    Counters
	Delete Counters
	PruneDel Counters // deletes on 's'-prefixed (node) keys within this module
}

func NewModuleCounters() *ModuleCounters {
	return &ModuleCounters{modules: make(map[string]*ModuleOps)}
}

func (mc *ModuleCounters) GetOrCreate(module string) *ModuleOps {
	mc.mu.RLock()
	ops, ok := mc.modules[module]
	mc.mu.RUnlock()
	if ok {
		return ops
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if ops, ok = mc.modules[module]; ok {
		return ops
	}
	ops = &ModuleOps{}
	mc.modules[module] = ops
	return ops
}

func (mc *ModuleCounters) ForEach(fn func(module string, ops *ModuleOps)) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	for mod, ops := range mc.modules {
		fn(mod, ops)
	}
}

// DBMetrics holds all counters for a single DB instance.
type DBMetrics struct {
	Name string // "application", "blockstore", "state"

	Get        Counters
	Set        Counters
	Delete     Counters
	BatchWrite Counters

	PruneDel   Counters // deletes on 's' or 'n' prefixed keys (pruning)
	StateDel   Counters // deletes on 'f' prefixed keys (state changes)

	Modules    *ModuleCounters // per-module attribution (application DB only)
}

func NewDBMetrics(name string) *DBMetrics {
	return &DBMetrics{
		Name:    name,
		Modules: NewModuleCounters(),
	}
}

var moduleStorePrefix = []byte("s/k:")

// extractModule extracts the module name from a key like "s/k:bank/..."
// Returns the module name or empty string if not a module-prefixed key.
func extractModule(key []byte) string {
	if !bytes.HasPrefix(key, moduleStorePrefix) {
		return ""
	}
	rest := key[len(moduleStorePrefix):]
	idx := bytes.IndexByte(rest, '/')
	if idx <= 0 {
		return ""
	}
	return string(rest[:idx])
}

// classifyDelete returns "prune", "state", or "other" based on the IAVL key
// type byte that follows the module prefix.
// Full key layout: s/k:<module>/<iavl_prefix><data>
// IAVL prefixes: 's' = node, 'f' = fast node, 'n' = legacy node, 'm' = metadata
func classifyDelete(key []byte) string {
	if !bytes.HasPrefix(key, moduleStorePrefix) {
		return "other"
	}
	rest := key[len(moduleStorePrefix):]
	slashIdx := bytes.IndexByte(rest, '/')
	if slashIdx < 0 || slashIdx+1 >= len(rest) {
		return "other"
	}
	iavlPrefix := rest[slashIdx+1]
	switch iavlPrefix {
	case 's', 'n': // node keys or legacy node keys — pruning
		return "prune"
	case 'f': // fast node keys — state changes
		return "state"
	default:
		return "other"
	}
}

// LevelDBStats holds parsed LevelDB property values.
type LevelDBStats struct {
	CompMemCount       uint64
	CompLevel0Count    uint64
	CompNonLevel0Count uint64
	CompSeekCount      uint64

	IOReadBytes  float64
	IOWriteBytes float64

	WriteDelayCount    int64
	WriteDelayDuration time.Duration
	WritePaused        bool

	AliveIterators  int64
	AliveSnapshots  int64
	CachedBlockSize int64

	LevelTables    []int
	LevelSizes     []float64 // in MB
	LevelCompTime  []float64 // compaction time in seconds per level
	LevelCompRead  []float64 // compaction read in MB per level
	LevelCompWrite []float64 // compaction write in MB per level
}

// PollLevelDBStats reads LevelDB properties from the underlying DB.
// Returns nil if the DB is not a GoLevelDB.
func PollLevelDBStats(db dbm.DB) *LevelDBStats {
	gdb := unwrapGoLevelDB(db)
	if gdb == nil {
		return nil
	}
	ldb := gdb.DB()
	stats := &LevelDBStats{}

	if val, err := ldb.GetProperty("leveldb.compcount"); err == nil {
		fmt.Sscanf(val, "MemComp:%d Level0Comp:%d NonLevel0Comp:%d SeekComp:%d",
			&stats.CompMemCount, &stats.CompLevel0Count,
			&stats.CompNonLevel0Count, &stats.CompSeekCount)
	}

	if val, err := ldb.GetProperty("leveldb.iostats"); err == nil {
		fmt.Sscanf(val, "Read(MB):%f Write(MB):%f",
			&stats.IOReadBytes, &stats.IOWriteBytes)
		stats.IOReadBytes *= 1048576
		stats.IOWriteBytes *= 1048576
	}

	if val, err := ldb.GetProperty("leveldb.writedelay"); err == nil {
		var delayStr string
		var paused string
		fmt.Sscanf(val, "DelayN:%d Delay:%s Paused:%s",
			&stats.WriteDelayCount, &delayStr, &paused)
		stats.WriteDelayDuration, _ = time.ParseDuration(delayStr)
		stats.WritePaused = paused == "true"
	}

	if val, err := ldb.GetProperty("leveldb.aliveiters"); err == nil {
		stats.AliveIterators, _ = strconv.ParseInt(val, 10, 64)
	}

	if val, err := ldb.GetProperty("leveldb.alivesnaps"); err == nil {
		stats.AliveSnapshots, _ = strconv.ParseInt(val, 10, 64)
	}

	if val, err := ldb.GetProperty("leveldb.cachedblock"); err == nil && val != "<nil>" {
		stats.CachedBlockSize, _ = strconv.ParseInt(val, 10, 64)
	}

	if val, err := ldb.GetProperty("leveldb.stats"); err == nil {
		parseStatsTable(val, stats)
	}

	return stats
}

// parseStatsTable parses the leveldb.stats output:
//
//	Level |   Tables   |    Size(MB)   |    Time(sec)  |    Read(MB)   |   Write(MB)
//	------+------------+---------------+---------------+---------------+---------------
//	  0   |          2 |       1.00000 |       0.50000 |       0.00000 |       2.00000
func parseStatsTable(statsStr string, stats *LevelDBStats) {
	lines := strings.Split(statsStr, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] == '-' || !strings.Contains(line, "|") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 6 {
			continue
		}
		levelStr := strings.TrimSpace(parts[0])
		if levelStr == "Level" || levelStr == "Total" {
			continue
		}
		tablesStr := strings.TrimSpace(parts[1])
		sizeStr := strings.TrimSpace(parts[2])
		timeStr := strings.TrimSpace(parts[3])
		readStr := strings.TrimSpace(parts[4])
		writeStr := strings.TrimSpace(parts[5])

		t, err1 := strconv.Atoi(tablesStr)
		s, err2 := strconv.ParseFloat(sizeStr, 64)
		ct, err3 := strconv.ParseFloat(timeStr, 64)
		cr, err4 := strconv.ParseFloat(readStr, 64)
		cw, err5 := strconv.ParseFloat(writeStr, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		stats.LevelTables = append(stats.LevelTables, t)
		stats.LevelSizes = append(stats.LevelSizes, s)
		if err3 == nil {
			stats.LevelCompTime = append(stats.LevelCompTime, ct)
		}
		if err4 == nil {
			stats.LevelCompRead = append(stats.LevelCompRead, cr)
		}
		if err5 == nil {
			stats.LevelCompWrite = append(stats.LevelCompWrite, cw)
		}
	}
}

// unwrapGoLevelDB attempts to find a *dbm.GoLevelDB in the DB chain
// (possibly wrapped in PrefixDB or our own InstrumentedDB).
func unwrapGoLevelDB(db dbm.DB) *dbm.GoLevelDB {
	switch d := db.(type) {
	case *dbm.GoLevelDB:
		return d
	case *InstrumentedDB:
		return unwrapGoLevelDB(d.inner)
	default:
		return nil
	}
}

// GetUnderlyingLevelDB returns the raw *leveldb.DB if available.
func GetUnderlyingLevelDB(db dbm.DB) *leveldb.DB {
	gdb := unwrapGoLevelDB(db)
	if gdb == nil {
		return nil
	}
	return gdb.DB()
}

// PollCmtLevelDBStats reads LevelDB properties from a CometBFT DB.
func PollCmtLevelDBStats(db cmtdb.DB) *LevelDBStats {
	ldb := unwrapCmtGoLevelDB(db)
	if ldb == nil {
		return nil
	}
	stats := &LevelDBStats{}

	if val, err := ldb.GetProperty("leveldb.compcount"); err == nil {
		fmt.Sscanf(val, "MemComp:%d Level0Comp:%d NonLevel0Comp:%d SeekComp:%d",
			&stats.CompMemCount, &stats.CompLevel0Count,
			&stats.CompNonLevel0Count, &stats.CompSeekCount)
	}

	if val, err := ldb.GetProperty("leveldb.iostats"); err == nil {
		fmt.Sscanf(val, "Read(MB):%f Write(MB):%f",
			&stats.IOReadBytes, &stats.IOWriteBytes)
		stats.IOReadBytes *= 1048576
		stats.IOWriteBytes *= 1048576
	}

	if val, err := ldb.GetProperty("leveldb.writedelay"); err == nil {
		var delayStr string
		var paused string
		fmt.Sscanf(val, "DelayN:%d Delay:%s Paused:%s",
			&stats.WriteDelayCount, &delayStr, &paused)
		stats.WriteDelayDuration, _ = time.ParseDuration(delayStr)
		stats.WritePaused = paused == "true"
	}

	if val, err := ldb.GetProperty("leveldb.aliveiters"); err == nil {
		stats.AliveIterators, _ = strconv.ParseInt(val, 10, 64)
	}

	if val, err := ldb.GetProperty("leveldb.alivesnaps"); err == nil {
		stats.AliveSnapshots, _ = strconv.ParseInt(val, 10, 64)
	}

	if val, err := ldb.GetProperty("leveldb.cachedblock"); err == nil && val != "<nil>" {
		stats.CachedBlockSize, _ = strconv.ParseInt(val, 10, 64)
	}

	if val, err := ldb.GetProperty("leveldb.stats"); err == nil {
		parseStatsTable(val, stats)
	}

	return stats
}

func unwrapCmtGoLevelDB(db cmtdb.DB) *leveldb.DB {
	switch d := db.(type) {
	case *cmtdb.GoLevelDB:
		return d.DB()
	case *InstrumentedCmtDB:
		return unwrapCmtGoLevelDB(d.inner)
	default:
		return nil
	}
}
