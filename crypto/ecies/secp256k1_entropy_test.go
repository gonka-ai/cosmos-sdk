// Exercise the Go 1.26 reader behavior regardless of the SDK module's Go version.
//go:debug cryptocustomrand=0

package ecies_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"testing"
	"testing/iotest"

	"github.com/cosmos/cosmos-sdk/crypto/ecies"
	sdksecp256k1 "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// Match the DKG seed stream, including non-consuming single-byte probes.
type seedReader struct {
	seed    []byte
	counter uint64
	buf     []byte
}

func (r *seedReader) Read(p []byte) (int, error) {
	if len(p) == 1 {
		probe := *r
		var two [2]byte
		_, err := probe.Read(two[:])
		p[0] = two[0]
		return 1, err
	}
	n := 0
	for n < len(p) {
		if len(r.buf) == 0 {
			var counter [8]byte
			binary.BigEndian.PutUint64(counter[:], r.counter)
			r.counter++
			block := sha256.Sum256(append(append([]byte(nil), r.seed...), counter[:]...))
			r.buf = block[:]
		}
		copied := copy(p[n:], r.buf)
		r.buf = r.buf[copied:]
		n += copied
	}
	return n, nil
}

func TestEncryptSecp256k1LegacyCiphertexts(t *testing.T) {
	// Generated with Go 1.25.9 using fixed test keys, shares, and seeds.
	data, err := os.ReadFile("testdata/legacy-ciphertexts.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors [][]struct{ Seed, Share, Ciphertext []byte }
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 4 {
		t.Fatalf("got %d dealers, want 4", len(vectors))
	}
	for dealer, row := range vectors {
		if len(row) != 4 {
			t.Fatalf("dealer %d: got %d recipients, want 4", dealer, len(row))
		}
		for recipient, vector := range row {
			t.Run(fmt.Sprintf("dealer_%d/recipient_%d", dealer, recipient), func(t *testing.T) {
				key := sdksecp256k1.GenPrivKeyFromSecret([]byte(fmt.Sprintf("dkg-complaint-participant-%d", recipient)))
				prv := ecies.ImportECDSA(secp256k1.PrivKeyFromBytes(key.Key).ToECDSA())
				first, err := ecies.Encrypt(&seedReader{seed: vector.Seed}, &prv.PublicKey, vector.Share, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				second, err := ecies.Encrypt(&seedReader{seed: vector.Seed}, &prv.PublicKey, vector.Share, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(first, second) {
					t.Errorf("same seed produced different ciphertexts:\nfirst  %x\nsecond %x", first, second)
				}
				if !bytes.Equal(first, vector.Ciphertext) {
					t.Errorf("Go 1.25.9 ciphertext mismatch:\ngot  %x\nwant %x", first, vector.Ciphertext)
				}
				plaintext, err := prv.Decrypt(vector.Ciphertext, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(plaintext, vector.Share) {
					t.Errorf("legacy ciphertext decrypted to %x, want %x", plaintext, vector.Share)
				}
			})
		}
	}
}

func TestSecp256k1LegacyEntropyLayout(t *testing.T) {
	// Reject zero and the order itself; accept one; leave the IV untouched.
	var valid [32]byte
	valid[31] = 1
	iv := bytes.Repeat([]byte{0xa5}, 16)
	stream := append(make([]byte, 32), secp256k1.S256().Params().N.Bytes()...)
	stream = append(stream, valid[:]...)
	stream = append(stream, iv...)
	r := bytes.NewReader(stream)
	key, err := ecies.GenerateKey(r, secp256k1.S256(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if key.D.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("wrong scalar: got %s, want 1", key.D)
	}
	remaining, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(iv, remaining) {
		t.Errorf("IV reader position changed: got %x, want %x", remaining, iv)
	}
}

func TestSecp256k1EntropyReadError(t *testing.T) {
	readError := errors.New("entropy read failed")
	for _, test := range []struct {
		name string
		r    io.Reader
		want error
	}{
		{"empty", bytes.NewReader(nil), io.EOF},
		{"short", bytes.NewReader(make([]byte, 31)), io.ErrUnexpectedEOF},
		{"reader_error", iotest.ErrReader(readError), readError},
	} {
		t.Run(test.name, func(t *testing.T) {
			key, err := ecies.GenerateKey(test.r, secp256k1.S256(), nil)
			if !errors.Is(err, test.want) || key != nil {
				t.Errorf("GenerateKey = (%v, %v), want (nil, %v)", key, err, test.want)
			}
		})
	}
}
