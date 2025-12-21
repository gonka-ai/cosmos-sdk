package keeper

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

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
	out1 := sortAndFilterComputeResult(ctx, in1, emptyByCons, emptyByOp)

	in2 := append([]ComputeResult(nil), computeResults...)
	out2 := sortAndFilterComputeResult(ctx, in2, emptyByCons, emptyByOp)

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

	out := sortAndFilterComputeResult(ctx, input, currentByCons, currentByOp)

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
