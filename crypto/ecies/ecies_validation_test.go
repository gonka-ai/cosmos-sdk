package ecies

import (
	"crypto/rand"
	"math/big"
	"testing"
)

// undersizedCiphertext builds a ciphertext whose MAC is valid but whose
// encrypted-message section is shorter than one cipher block. Anyone holding
// the recipient's public key can produce this, since the MAC key is derived
// from the sender's ephemeral key agreement.
func undersizedCiphertext(t *testing.T, pub *PublicKey, emLen int) []byte {
	t.Helper()
	params, err := pubkeyParams(pub)
	if err != nil {
		t.Fatal(err)
	}
	R, err := GenerateKey(rand.Reader, pub.Curve, params)
	if err != nil {
		t.Fatal(err)
	}
	z, err := R.GenerateShared(pub, params.KeyLen, params.KeyLen)
	if err != nil {
		t.Fatal(err)
	}
	_, Km := deriveKeys(params.Hash(), z, nil, params.KeyLen)

	em := make([]byte, emLen)
	if _, err := rand.Read(em); err != nil {
		t.Fatal(err)
	}
	d := messageTag(params.Hash, Km, em, nil)

	Rb := Marshal(pub.Curve, R.PublicKey.X, R.PublicKey.Y)
	ct := make([]byte, 0, len(Rb)+len(em)+len(d))
	ct = append(ct, Rb...)
	ct = append(ct, em...)
	ct = append(ct, d...)
	return ct
}

func TestDecryptRejectsUndersizedMessageWithValidMAC(t *testing.T) {
	prv, err := GenerateKey(rand.Reader, DefaultCurve, nil)
	if err != nil {
		t.Fatal(err)
	}
	params := prv.PublicKey.Params

	for emLen := 1; emLen < params.BlockSize; emLen++ {
		ct := undersizedCiphertext(t, &prv.PublicKey, emLen)
		_, err := prv.Decrypt(ct, nil, nil)
		if err != ErrInvalidMessage {
			t.Fatalf("emLen=%d (ct=%d bytes): want ErrInvalidMessage, got %v", emLen, len(ct), err)
		}
	}
}

// The smallest ciphertext Encrypt produces carries a one-byte message; it
// must stay above the length floor.
func TestDecryptAcceptsOneByteMessage(t *testing.T) {
	prv, err := GenerateKey(rand.Reader, DefaultCurve, nil)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := Encrypt(rand.Reader, &prv.PublicKey, []byte{0x42}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := prv.Decrypt(ct, nil, nil)
	if err != nil {
		t.Fatalf("ct=%d bytes: %v", len(ct), err)
	}
	if len(m) != 1 || m[0] != 0x42 {
		t.Fatalf("want [0x42], got %x", m)
	}
}

func TestGenerateSharedRejectsOffCurvePoint(t *testing.T) {
	prv, err := GenerateKey(rand.Reader, DefaultCurve, nil)
	if err != nil {
		t.Fatal(err)
	}
	offCurve := &PublicKey{
		X:      big.NewInt(1),
		Y:      big.NewInt(1),
		Curve:  DefaultCurve,
		Params: prv.PublicKey.Params,
	}
	if DefaultCurve.IsOnCurve(offCurve.X, offCurve.Y) {
		t.Fatal("test point unexpectedly lies on the curve")
	}
	if _, err := prv.GenerateShared(offCurve, 16, 16); err != ErrInvalidPublicKey {
		t.Fatalf("want ErrInvalidPublicKey, got %v", err)
	}
	nilCoord := &PublicKey{Curve: DefaultCurve, Params: prv.PublicKey.Params}
	if _, err := prv.GenerateShared(nilCoord, 16, 16); err != ErrInvalidPublicKey {
		t.Fatalf("nil coordinates: want ErrInvalidPublicKey, got %v", err)
	}
}

func TestDecryptRejectsOffCurveEphemeralKey(t *testing.T) {
	prv, err := GenerateKey(rand.Reader, DefaultCurve, nil)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := Encrypt(rand.Reader, &prv.PublicKey, []byte("Hello, world."), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Overwrite the ephemeral point R with (1, 1), which is not on secp256k1.
	byteLen := (DefaultCurve.Params().BitSize + 7) >> 3
	for i := 1; i < 1+2*byteLen; i++ {
		ct[i] = 0
	}
	ct[byteLen] = 1
	ct[2*byteLen] = 1

	if _, err := prv.Decrypt(ct, nil, nil); err != ErrInvalidPublicKey {
		t.Fatalf("want ErrInvalidPublicKey, got %v", err)
	}
}
