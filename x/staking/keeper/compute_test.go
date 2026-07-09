package keeper

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

type computeResultJSON struct {
	ValidatorPubKey struct {
		Key string `json:"key"`
	} `json:"ValidatorPubKey"`

	OperatorAddress string `json:"OperatorAddress"`
	Power           int64  `json:"Power"`
}

func mustEd25519PubKey(t *testing.T, seed byte) cryptotypes.PubKey {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed
	}
	return &ed25519.PubKey{Key: key}
}

func TestSortAndFilterComputeResult_FromJSON(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate test file via runtime.Caller")
	}
	jsonPath := filepath.Join(filepath.Dir(thisFile), "compute_test.json")

	bz, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("failed reading %s: %v", jsonPath, err)
	}

	var raw []computeResultJSON
	if err := json.Unmarshal(bz, &raw); err != nil {
		t.Fatalf("failed unmarshalling %s: %v", jsonPath, err)
	}

	computeResults := make([]ComputeResult, 0, len(raw))
	for i := range raw {
		var pk *ed25519.PubKey
		if raw[i].ValidatorPubKey.Key != "" {
			keyBz, err := base64.StdEncoding.DecodeString(raw[i].ValidatorPubKey.Key)
			if err != nil {
				t.Fatalf("invalid base64 pubkey at index %d: %v", i, err)
			}
			pk = &ed25519.PubKey{Key: keyBz}
		}

		var pubKey cryptotypes.PubKey
		if pk != nil {
			pubKey = pk
		}

		computeResults = append(computeResults, ComputeResult{
			Power:           raw[i].Power,
			ValidatorPubKey: pubKey,
			OperatorAddress: raw[i].OperatorAddress,
		})
	}

	key := storetypes.NewKVStoreKey("compute_test")
	tkey := storetypes.NewTransientStoreKey("transient_compute_test")
	ctx := testutil.DefaultContext(key, tkey)

	emptyByCons := map[string]types.Validator{}
	emptyByOp := map[string]types.Validator{}

	// Run twice to ensure deterministic output (function sorts/filters internally).
	in1 := append([]ComputeResult(nil), computeResults...)
	out1, _ := sortAndFilterComputeResult(ctx, in1, emptyByCons, emptyByOp)

	in2 := append([]ComputeResult(nil), computeResults...)
	out2, _ := sortAndFilterComputeResult(ctx, in2, emptyByCons, emptyByOp)

	if len(out1) != len(out2) {
		t.Fatalf("non-deterministic output length: first=%d second=%d", len(out1), len(out2))
	}
	for i := range out1 {
		if out1[i].OperatorAddress != out2[i].OperatorAddress || out1[i].Power != out2[i].Power {
			t.Fatalf("non-deterministic output at index %d: %+v vs %+v", i, out1[i], out2[i])
		}
		a1, a2 := "", ""
		if out1[i].ValidatorPubKey != nil {
			a1 = out1[i].ValidatorPubKey.Address().String()
		}
		if out2[i].ValidatorPubKey != nil {
			a2 = out2[i].ValidatorPubKey.Address().String()
		}
		if a1 != a2 {
			t.Fatalf("non-deterministic pubkey at index %d: %s vs %s", i, a1, a2)
		}
	}

	// Invariants expected after filtering.
	seenOp := map[string]struct{}{}
	seenCons := map[string]struct{}{}
	for i := range out1 {
		res := out1[i]
		if res.OperatorAddress == "" {
			t.Fatalf("empty operator address at index %d", i)
		}
		if res.ValidatorPubKey == nil {
			t.Fatalf("nil pubkey at index %d (operator=%s)", i, res.OperatorAddress)
		}
		if res.Power <= 0 {
			t.Fatalf("non-positive power at index %d (operator=%s power=%d)", i, res.OperatorAddress, res.Power)
		}

		if _, exists := seenOp[res.OperatorAddress]; exists {
			t.Fatalf("duplicate operator address in output: %s", res.OperatorAddress)
		}
		seenOp[res.OperatorAddress] = struct{}{}

		consAddr := res.ValidatorPubKey.Address().String()
		if _, exists := seenCons[consAddr]; exists {
			t.Fatalf("duplicate consensus key in output: %s", consAddr)
		}
		seenCons[consAddr] = struct{}{}
	}

	// Sorting expectation: by operator address asc, then pubkey address asc, then power desc.
	for i := 1; i < len(out1); i++ {
		prev, cur := out1[i-1], out1[i]

		if prev.OperatorAddress > cur.OperatorAddress {
			t.Fatalf("not sorted by operator address at %d: %s > %s", i, prev.OperatorAddress, cur.OperatorAddress)
		}
		if prev.OperatorAddress != cur.OperatorAddress {
			continue
		}

		prevKey := prev.ValidatorPubKey.Address().String()
		curKey := cur.ValidatorPubKey.Address().String()
		if prevKey > curKey {
			t.Fatalf("not sorted by pubkey at %d: %s > %s (operator=%s)", i, prevKey, curKey, cur.OperatorAddress)
		}
		if prevKey != curKey {
			continue
		}

		if prev.Power < cur.Power {
			t.Fatalf("not sorted by power desc at %d: %d < %d (operator=%s pubkey=%s)", i, prev.Power, cur.Power, cur.OperatorAddress, curKey)
		}
	}
}

func TestSortAndFilterComputeResult_FiltersAgainstExisting(t *testing.T) {
	t.Parallel()

	key := storetypes.NewKVStoreKey("compute_test_small")
	tkey := storetypes.NewTransientStoreKey("transient_compute_test_small")
	sdkCtx := testutil.DefaultContext(key, tkey)
	ctx := sdk.WrapSDKContext(sdkCtx)

	// Existing validator: op1 is already bound to pk1.
	pk1 := mustEd25519PubKey(t, 0x01)
	pk2 := mustEd25519PubKey(t, 0x02)
	pk3 := mustEd25519PubKey(t, 0x03)
	pk4 := mustEd25519PubKey(t, 0x04)

	existing, err := types.NewValidator("op1", pk1, types.Description{Moniker: "op1"})
	if err != nil {
		t.Fatalf("failed to create existing validator: %v", err)
	}
	// Give the existing validator non-zero tokens so the conflict-rejection assertions
	// in this test still apply (GON-191 only relaxes rejection when tokens are zero).
	existing.Tokens = math.NewInt(10)

	currentByCons := map[string]types.Validator{
		pk1.Address().String(): existing,
	}
	currentByOp := map[string]types.Validator{
		"op1": existing,
	}

	// Inputs exercise:
	// - invalid entries (empty operator / nil pubkey / non-positive power)
	// - reject same consensus key assigned to a different operator (op2 + pk1)
	// - reject operator that changed consensus key (op1 + pk2)
	// - dedupe operator entries (op3 appears twice)
	// - dedupe consensus key entries (pk4 used by op5 and op6)
	input := []ComputeResult{
		{OperatorAddress: "", ValidatorPubKey: pk1, Power: 10},    // invalid (empty operator)
		{OperatorAddress: "op2", ValidatorPubKey: pk1, Power: 10}, // rejected: pk1 already owned by op1
		{OperatorAddress: "op1", ValidatorPubKey: pk2, Power: 10}, // rejected: op1 changed consensus key
		{OperatorAddress: "op1", ValidatorPubKey: pk1, Power: 10}, // valid
		{OperatorAddress: "op3", ValidatorPubKey: pk2, Power: 10}, // op3 duplicate entry (will be compared with next)
		{OperatorAddress: "op3", ValidatorPubKey: pk3, Power: 11}, // op3 duplicate entry
		{OperatorAddress: "op4", ValidatorPubKey: nil, Power: 10}, // invalid (nil pubkey)
		{OperatorAddress: "op5", ValidatorPubKey: pk4, Power: 10}, // pk4 duplicate with op6
		{OperatorAddress: "op6", ValidatorPubKey: pk4, Power: 10}, // pk4 duplicate with op5
		{OperatorAddress: "op7", ValidatorPubKey: pk3, Power: 0},  // invalid (non-positive power)
		{OperatorAddress: "op8", ValidatorPubKey: pk3, Power: -1}, // invalid (non-positive power)
	}

	out, _ := sortAndFilterComputeResult(ctx, input, currentByCons, currentByOp)

	// Expect: op1 survives with pk1; op3 survives with the lexicographically smallest pubkey addr among (pk2, pk3);
	// and only one of (op5, op6) survives for pk4 (the earlier operator in sort order).
	wantOps := map[string]struct{}{
		"op1": {},
		"op3": {},
		"op5": {}, // because "op5" sorts before "op6" and both share pk4
	}
	if len(out) != len(wantOps) {
		t.Fatalf("unexpected output size: got=%d want=%d output=%+v", len(out), len(wantOps), out)
	}

	seenOps := map[string]struct{}{}
	seenCons := map[string]struct{}{}
	for _, res := range out {
		if _, ok := wantOps[res.OperatorAddress]; !ok {
			t.Fatalf("unexpected operator in output: %s", res.OperatorAddress)
		}
		if res.ValidatorPubKey == nil || res.Power <= 0 || res.OperatorAddress == "" {
			t.Fatalf("output contains invalid entry: %+v", res)
		}
		if _, exists := seenOps[res.OperatorAddress]; exists {
			t.Fatalf("duplicate operator in output: %s", res.OperatorAddress)
		}
		seenOps[res.OperatorAddress] = struct{}{}

		cons := res.ValidatorPubKey.Address().String()
		if _, exists := seenCons[cons]; exists {
			t.Fatalf("duplicate consensus key in output: %s", cons)
		}
		seenCons[cons] = struct{}{}
	}

	// Specifically ensure op1 kept pk1.
	for _, res := range out {
		if res.OperatorAddress == "op1" {
			if res.ValidatorPubKey.Address().String() != pk1.Address().String() {
				t.Fatalf("op1 did not keep existing consensus key: got=%s want=%s", res.ValidatorPubKey.Address().String(), pk1.Address().String())
			}
		}
	}
}

// TestFilterBasedOnExisting_StaleConsensusKeyConflict covers GON-191 Change 1:
// when a new operator claims a consensus key that is still held by a stale
// (tokens=0) validator owned by a different operator, the filter should allow
// the new entry and report the stale validator for deletion.
func TestFilterBasedOnExisting_StaleConsensusKeyConflict(t *testing.T) {
	t.Parallel()

	key := storetypes.NewKVStoreKey("compute_test_stale_cons")
	tkey := storetypes.NewTransientStoreKey("transient_compute_test_stale_cons")
	sdkCtx := testutil.DefaultContext(key, tkey)
	ctx := sdk.WrapSDKContext(sdkCtx)

	sharedKey := mustEd25519PubKey(t, 0x10)

	// Stale validator: registered to opStale, but with tokens=0 (e.g., decommissioned).
	stale, err := types.NewValidator("opStale", sharedKey, types.Description{Moniker: "opStale"})
	if err != nil {
		t.Fatalf("failed to create stale validator: %v", err)
	}
	// Tokens already zero by default; assert that explicitly so the intent is clear.
	if !stale.Tokens.IsZero() {
		t.Fatalf("expected stale validator to have zero tokens by default, got %s", stale.Tokens)
	}

	currentByCons := map[string]types.Validator{
		sharedKey.Address().String(): stale,
	}
	currentByOp := map[string]types.Validator{
		"opStale": stale,
	}

	input := []ComputeResult{
		{OperatorAddress: "opNew", ValidatorPubKey: sharedKey, Power: 10},
	}

	out, staleToRemove := filterBasedOnExisting(ctx, input, currentByCons, currentByOp)

	if len(out) != 1 || out[0].OperatorAddress != "opNew" {
		t.Fatalf("expected new operator to pass through filter, got %+v", out)
	}
	if len(staleToRemove) != 1 || staleToRemove[0].OperatorAddress != "opStale" {
		t.Fatalf("expected stale validator opStale to be returned for deletion, got %+v", staleToRemove)
	}
	// The filter must have removed the stale entries from the working maps so the
	// downstream create path treats opNew as a fresh validator.
	if _, exists := currentByCons[sharedKey.Address().String()]; exists {
		t.Fatalf("stale entry still present in currentByCons after filter")
	}
	if _, exists := currentByOp["opStale"]; exists {
		t.Fatalf("stale entry still present in currentByOp after filter")
	}
}

// TestFilterBasedOnExisting_NonStaleConsensusKeyConflict ensures the rejection path
// is preserved for active validators: an active (tokens>0) validator's consensus key
// cannot be hijacked by a different operator.
func TestFilterBasedOnExisting_NonStaleConsensusKeyConflict(t *testing.T) {
	t.Parallel()

	key := storetypes.NewKVStoreKey("compute_test_active_cons")
	tkey := storetypes.NewTransientStoreKey("transient_compute_test_active_cons")
	sdkCtx := testutil.DefaultContext(key, tkey)
	ctx := sdk.WrapSDKContext(sdkCtx)

	sharedKey := mustEd25519PubKey(t, 0x11)

	active, err := types.NewValidator("opActive", sharedKey, types.Description{Moniker: "opActive"})
	if err != nil {
		t.Fatalf("failed to create active validator: %v", err)
	}
	active.Tokens = math.NewInt(100)

	currentByCons := map[string]types.Validator{
		sharedKey.Address().String(): active,
	}
	currentByOp := map[string]types.Validator{
		"opActive": active,
	}

	input := []ComputeResult{
		{OperatorAddress: "opAttacker", ValidatorPubKey: sharedKey, Power: 10},
	}

	out, staleToRemove := filterBasedOnExisting(ctx, input, currentByCons, currentByOp)

	if len(out) != 0 {
		t.Fatalf("expected attacker entry to be rejected when active validator owns the key, got %+v", out)
	}
	if len(staleToRemove) != 0 {
		t.Fatalf("expected no stale removals for active conflict, got %+v", staleToRemove)
	}
	if _, exists := currentByCons[sharedKey.Address().String()]; !exists {
		t.Fatalf("active entry must remain in currentByCons after rejection")
	}
}

// TestFilterBasedOnExisting_StaleOperatorKeyChange covers GON-191 Change 2:
// when the same operator submits a compute result with a different consensus key
// and the existing entry has tokens=0, the filter should allow the key change and
// report the stale entry for deletion.
func TestFilterBasedOnExisting_StaleOperatorKeyChange(t *testing.T) {
	t.Parallel()

	key := storetypes.NewKVStoreKey("compute_test_stale_op_change")
	tkey := storetypes.NewTransientStoreKey("transient_compute_test_stale_op_change")
	sdkCtx := testutil.DefaultContext(key, tkey)
	ctx := sdk.WrapSDKContext(sdkCtx)

	oldKey := mustEd25519PubKey(t, 0x20)
	newKey := mustEd25519PubKey(t, 0x21)

	stale, err := types.NewValidator("opSame", oldKey, types.Description{Moniker: "opSame"})
	if err != nil {
		t.Fatalf("failed to create stale validator: %v", err)
	}
	if !stale.Tokens.IsZero() {
		t.Fatalf("expected stale validator to have zero tokens by default, got %s", stale.Tokens)
	}

	currentByCons := map[string]types.Validator{
		oldKey.Address().String(): stale,
	}
	currentByOp := map[string]types.Validator{
		"opSame": stale,
	}

	input := []ComputeResult{
		{OperatorAddress: "opSame", ValidatorPubKey: newKey, Power: 10},
	}

	out, staleToRemove := filterBasedOnExisting(ctx, input, currentByCons, currentByOp)

	if len(out) != 1 || out[0].OperatorAddress != "opSame" || out[0].ValidatorPubKey.Address().String() != newKey.Address().String() {
		t.Fatalf("expected key change to pass through filter, got %+v", out)
	}
	if len(staleToRemove) != 1 || staleToRemove[0].OperatorAddress != "opSame" {
		t.Fatalf("expected stale opSame to be returned for deletion, got %+v", staleToRemove)
	}
	if _, exists := currentByOp["opSame"]; exists {
		t.Fatalf("stale entry still present in currentByOp after filter")
	}
}

// TestFilterBasedOnExisting_NonStaleOperatorKeyChange ensures an active operator
// cannot silently swap its consensus key — that path must still be rejected to
// preserve the existing safety property that consensus-key changes for live
// validators are not allowed.
func TestFilterBasedOnExisting_NonStaleOperatorKeyChange(t *testing.T) {
	t.Parallel()

	key := storetypes.NewKVStoreKey("compute_test_active_op_change")
	tkey := storetypes.NewTransientStoreKey("transient_compute_test_active_op_change")
	sdkCtx := testutil.DefaultContext(key, tkey)
	ctx := sdk.WrapSDKContext(sdkCtx)

	oldKey := mustEd25519PubKey(t, 0x30)
	newKey := mustEd25519PubKey(t, 0x31)

	active, err := types.NewValidator("opSame", oldKey, types.Description{Moniker: "opSame"})
	if err != nil {
		t.Fatalf("failed to create active validator: %v", err)
	}
	active.Tokens = math.NewInt(100)

	currentByCons := map[string]types.Validator{
		oldKey.Address().String(): active,
	}
	currentByOp := map[string]types.Validator{
		"opSame": active,
	}

	input := []ComputeResult{
		{OperatorAddress: "opSame", ValidatorPubKey: newKey, Power: 10},
	}

	out, staleToRemove := filterBasedOnExisting(ctx, input, currentByCons, currentByOp)

	if len(out) != 0 {
		t.Fatalf("expected key change for active validator to be rejected, got %+v", out)
	}
	if len(staleToRemove) != 0 {
		t.Fatalf("expected no stale removals for active key-change rejection, got %+v", staleToRemove)
	}
}

// TestFilterBasedOnExisting_StaleKeyChangeNoGhostDoubleRemoval is the regression
// test for GLiberman's review on PR #14: when Change 2 (operator changes consensus
// key) allows a zero-power validator through, the stale entry must be dropped
// from BOTH the operator map and the consensus-key map. If only the operator map
// is pruned, a later compute result in the same batch that happens to reference
// the old consensus key would find the ghost entry, see tokens=0, and queue the
// same stale validator for deletion a second time.
func TestFilterBasedOnExisting_StaleKeyChangeNoGhostDoubleRemoval(t *testing.T) {
	t.Parallel()

	key := storetypes.NewKVStoreKey("compute_test_ghost_double_removal")
	tkey := storetypes.NewTransientStoreKey("transient_compute_test_ghost_double_removal")
	sdkCtx := testutil.DefaultContext(key, tkey)
	ctx := sdk.WrapSDKContext(sdkCtx)

	oldKey := mustEd25519PubKey(t, 0x40)
	newKey := mustEd25519PubKey(t, 0x41)

	// Stale validator opSame currently registered with oldKey, tokens=0.
	stale, err := types.NewValidator("opSame", oldKey, types.Description{Moniker: "opSame"})
	if err != nil {
		t.Fatalf("failed to create stale validator: %v", err)
	}

	currentByCons := map[string]types.Validator{
		oldKey.Address().String(): stale,
	}
	currentByOp := map[string]types.Validator{
		"opSame": stale,
	}

	// Two compute results in the same batch:
	//   1. opSame switching from oldKey to newKey (Change 2 path, stale allowed)
	//   2. opOther claiming oldKey for itself (Change 1 path, would find ghost)
	// Without the consensus-key-map cleanup, the second entry would re-discover
	// the stale validator and append it to staleToRemove a second time.
	input := []ComputeResult{
		{OperatorAddress: "opSame", ValidatorPubKey: newKey, Power: 10},
		{OperatorAddress: "opOther", ValidatorPubKey: oldKey, Power: 10},
	}

	out, staleToRemove := filterBasedOnExisting(ctx, input, currentByCons, currentByOp)

	if len(out) != 2 {
		t.Fatalf("expected both compute results to pass through filter, got %+v", out)
	}
	if len(staleToRemove) != 1 {
		t.Fatalf("expected stale validator to be queued for removal exactly once, got %d entries: %+v", len(staleToRemove), staleToRemove)
	}
	if staleToRemove[0].OperatorAddress != "opSame" {
		t.Fatalf("expected stale opSame in removal list, got %+v", staleToRemove[0])
	}
}
