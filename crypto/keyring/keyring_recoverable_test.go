package keyring

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func newTestKeyring(t *testing.T) Keyring {
	t.Helper()
	cdc := codec.NewProtoCodec(nil)
	kr := NewInMemory(cdc)
	return kr
}

func TestSignRecoverableRoundtrip(t *testing.T) {
	kr := newTestKeyring(t)

	_, _, err := kr.NewMnemonic("alice", English, sdk.FullFundraiserPath, DefaultBIP39Passphrase, hd.Secp256k1)
	require.NoError(t, err)

	msg := []byte("hello recoverable world")

	// Sign with recoverable signature.
	sig, err := kr.SignRecoverable("alice", msg)
	require.NoError(t, err)
	require.Len(t, sig, 65, "recoverable signature must be 65 bytes")

	// Recover the public key and verify it matches the stored key.
	recoveredPub, err := RecoverPubKey(msg, sig)
	require.NoError(t, err)

	rec, err := kr.Key("alice")
	require.NoError(t, err)
	storedPub, err := rec.GetPubKey()
	require.NoError(t, err)

	require.Equal(t, storedPub.Bytes(), recoveredPub.Bytes(), "recovered pubkey must match stored pubkey")
}

func TestRecoverAddressRoundtrip(t *testing.T) {
	kr := newTestKeyring(t)

	_, _, err := kr.NewMnemonic("bob", English, sdk.FullFundraiserPath, DefaultBIP39Passphrase, hd.Secp256k1)
	require.NoError(t, err)

	msg := []byte("address recovery test message")

	sig, err := kr.SignRecoverable("bob", msg)
	require.NoError(t, err)

	recoveredAddr, err := RecoverAddress(msg, sig)
	require.NoError(t, err)

	rec, err := kr.Key("bob")
	require.NoError(t, err)
	storedPub, err := rec.GetPubKey()
	require.NoError(t, err)
	expectedAddr := sdk.AccAddress(storedPub.Address())

	require.Equal(t, expectedAddr, recoveredAddr, "recovered address must match stored address")
}

func TestSignRecoverableByAddress(t *testing.T) {
	kr := newTestKeyring(t)

	_, _, err := kr.NewMnemonic("carol", English, sdk.FullFundraiserPath, DefaultBIP39Passphrase, hd.Secp256k1)
	require.NoError(t, err)

	rec, err := kr.Key("carol")
	require.NoError(t, err)
	pub, err := rec.GetPubKey()
	require.NoError(t, err)
	addr := sdk.AccAddress(pub.Address())

	msg := []byte("sign by address test")

	sig, err := kr.SignRecoverableByAddress(addr, msg)
	require.NoError(t, err)
	require.Len(t, sig, 65)

	recoveredAddr, err := RecoverAddress(msg, sig)
	require.NoError(t, err)
	require.Equal(t, addr, recoveredAddr)
}

func TestRecoverPubKeyWrongMessage(t *testing.T) {
	kr := newTestKeyring(t)

	_, _, err := kr.NewMnemonic("dave", English, sdk.FullFundraiserPath, DefaultBIP39Passphrase, hd.Secp256k1)
	require.NoError(t, err)

	msg := []byte("original message")
	wrongMsg := []byte("tampered message")

	sig, err := kr.SignRecoverable("dave", msg)
	require.NoError(t, err)

	// RecoverCompact will still return a key for a wrong message, but it won't match
	// the signer's actual key. Verify it's a different public key.
	rec, err := kr.Key("dave")
	require.NoError(t, err)
	storedPub, err := rec.GetPubKey()
	require.NoError(t, err)

	recoveredPub, err := RecoverPubKey(wrongMsg, sig)
	require.NoError(t, err) // recovery itself succeeds; just recovers wrong key
	require.NotEqual(t, storedPub.Bytes(), recoveredPub.Bytes(),
		"wrong message must recover a different (wrong) public key")
}
