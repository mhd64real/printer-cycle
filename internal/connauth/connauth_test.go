package connauth_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/connauth"
)

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestAValidProofAuthenticates(t *testing.T) {
	pub, priv := keypair(t)

	ch, err := connauth.NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Nonce()) != connauth.NonceSize {
		t.Fatalf("nonce is %d bytes, want %d", len(ch.Nonce()), connauth.NonceSize)
	}

	proof, err := connauth.Sign(priv, ch.Nonce())
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Verify(pub, proof); err != nil {
		t.Errorf("a correct proof was rejected: %v", err)
	}
}

// A proof from one connection must be worthless on another. Otherwise anybody
// who captured a single successful handshake could replay it forever.
func TestAProofFromAnotherConnectionIsRefused(t *testing.T) {
	pub, priv := keypair(t)

	first, err := connauth.NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	second, err := connauth.NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Nonce(), second.Nonce()) {
		t.Fatal("two challenges produced the same nonce")
	}

	proof, err := connauth.Sign(priv, first.Nonce())
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Verify(pub, proof); !errors.Is(err, connauth.ErrBadSignature) {
		t.Errorf("a proof from another connection gave %v, want ErrBadSignature", err)
	}
}

// The domain prefix is what stops a signature collected here from meaning
// something in another protocol. A proof over the bare nonce must not verify,
// or the prefix is decorative.
func TestASignatureWithoutTheDomainPrefixIsRefused(t *testing.T) {
	pub, priv := keypair(t)

	ch, err := connauth.NewChallenge()
	if err != nil {
		t.Fatal(err)
	}

	bare := ed25519.Sign(priv, ch.Nonce())
	if err := ch.Verify(pub, bare); !errors.Is(err, connauth.ErrBadSignature) {
		t.Errorf("a signature over the bare nonce gave %v, want ErrBadSignature", err)
	}
}

// Message is the contract connector authors in other languages implement, so
// its exact bytes matter more than most things in this repository.
func TestMessageLayout(t *testing.T) {
	nonce := bytes.Repeat([]byte{0xAB}, connauth.NonceSize)
	msg := connauth.Message(nonce)

	const domain = "printer-cycle-connector-auth-v1"
	if !bytes.HasPrefix(msg, []byte(domain)) {
		t.Fatalf("message does not begin with the domain string")
	}
	if msg[len(domain)] != 0 {
		t.Errorf("byte after the domain is %d, want a zero separator", msg[len(domain)])
	}
	if !bytes.Equal(msg[len(domain)+1:], nonce) {
		t.Error("the nonce does not follow the separator unchanged")
	}
	if len(msg) != len(domain)+1+connauth.NonceSize {
		t.Errorf("message is %d bytes, want %d", len(msg), len(domain)+1+connauth.NonceSize)
	}
}

// One challenge answers one attempt. A failed attempt that left the nonce usable
// would turn a single connection into unlimited guesses.
func TestAChallengeAnswersOnlyOnce(t *testing.T) {
	pub, priv := keypair(t)

	ch, err := connauth.NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	proof, err := connauth.Sign(priv, ch.Nonce())
	if err != nil {
		t.Fatal(err)
	}

	if err := ch.Verify(pub, proof); err != nil {
		t.Fatal(err)
	}
	if err := ch.Verify(pub, proof); !errors.Is(err, connauth.ErrSpent) {
		t.Errorf("a second attempt on the same challenge gave %v, want ErrSpent", err)
	}

	// Spent by a failure too, not only by a success.
	other, err := connauth.NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Verify(pub, make([]byte, ed25519.SignatureSize)); !errors.Is(err, connauth.ErrBadSignature) {
		t.Fatalf("garbage proof gave %v", err)
	}
	good, _ := connauth.Sign(priv, other.Nonce())
	if err := other.Verify(pub, good); !errors.Is(err, connauth.ErrSpent) {
		t.Errorf("a challenge stayed usable after a failed attempt: %v", err)
	}
}

func TestWrongKeyIsRefused(t *testing.T) {
	_, priv := keypair(t)
	otherPub, _ := keypair(t)

	ch, err := connauth.NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	proof, err := connauth.Sign(priv, ch.Nonce())
	if err != nil {
		t.Fatal(err)
	}

	if err := ch.Verify(otherPub, proof); !errors.Is(err, connauth.ErrBadSignature) {
		t.Errorf("a proof checked against the wrong key gave %v, want ErrBadSignature", err)
	}
}

func TestMalformedInputsAreRefused(t *testing.T) {
	pub, priv := keypair(t)

	for name, proof := range map[string][]byte{
		"empty":     {},
		"truncated": make([]byte, ed25519.SignatureSize-1),
		"oversized": make([]byte, ed25519.SignatureSize+1),
	} {
		ch, err := connauth.NewChallenge()
		if err != nil {
			t.Fatal(err)
		}
		if err := ch.Verify(pub, proof); !errors.Is(err, connauth.ErrBadSignature) {
			t.Errorf("%s proof gave %v, want ErrBadSignature", name, err)
		}
	}

	ch, err := connauth.NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	proof, _ := connauth.Sign(priv, ch.Nonce())
	if err := ch.Verify(ed25519.PublicKey("short"), proof); err == nil {
		t.Error("a public key of the wrong length was accepted")
	}

	if _, err := connauth.Sign(ed25519.PrivateKey("short"), make([]byte, connauth.NonceSize)); err == nil {
		t.Error("Sign accepted a private key of the wrong length")
	}
	if _, err := connauth.Sign(priv, []byte("short nonce")); err == nil {
		t.Error("Sign accepted a nonce of the wrong length")
	}
}
