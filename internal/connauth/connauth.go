// Package connauth authenticates connectors to core.
//
// A connector proves who it is by signing a per-connection nonce with an Ed25519
// private key. Core holds only the matching public key, so it never possesses
// anything capable of impersonating a connector and a copied database is worth
// nothing to whoever took it.
package connauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
)

// domain separates printer-cycle's signatures from every other use of the same
// key.
//
// A connector signs bytes chosen by whatever it is connected to. Without a
// prefix, a hostile core could hand over a nonce that is really a message in
// some other protocol and collect a valid signature over it. The prefix costs
// one comparison and closes that off completely.
//
// It carries a version so the scheme can change without old signatures becoming
// valid under new rules.
const domain = "printer-cycle-connector-auth-v1"

// NonceSize is the length of a challenge nonce in bytes.
const NonceSize = 32

var (
	ErrBadSignature = errors.New("connauth: signature does not verify")
	ErrSpent        = errors.New("connauth: this challenge has already been answered")
)

// Message is the exact byte sequence a connector signs.
//
// Exported because every connector author needs it, in whatever language, and
// the definition should come from one place rather than from each of them
// reading the specification and hoping.
func Message(nonce []byte) []byte {
	msg := make([]byte, 0, len(domain)+1+len(nonce))
	msg = append(msg, domain...)
	msg = append(msg, 0)
	msg = append(msg, nonce...)
	return msg
}

// Sign produces the proof a connector sends in its authenticate call.
func Sign(key ed25519.PrivateKey, nonce []byte) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("connauth: private key is %d bytes, want %d",
			len(key), ed25519.PrivateKeySize)
	}
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("connauth: nonce is %d bytes, want %d", len(nonce), NonceSize)
	}
	return ed25519.Sign(key, Message(nonce)), nil
}

// Challenge is one connection's authentication attempt.
//
// Each connection gets a fresh nonce, and each nonce answers exactly one
// attempt. Both matter: a nonce reused across connections would let a signature
// captured once be replayed forever, and a nonce reusable within a connection
// would let an attacker who obtained a single valid proof retry it after being
// disconnected.
type Challenge struct {
	nonce []byte

	mu    sync.Mutex
	spent bool
}

// NewChallenge creates a challenge with a fresh random nonce.
func NewChallenge() (*Challenge, error) {
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("connauth: reading random nonce: %w", err)
	}
	return &Challenge{nonce: nonce}, nil
}

// Nonce returns the bytes to send in the hello notification.
func (c *Challenge) Nonce() []byte {
	out := make([]byte, len(c.nonce))
	copy(out, c.nonce)
	return out
}

// Verify checks a proof against a connector's public key.
//
// The challenge is spent whether or not verification succeeds. A failed attempt
// that left the nonce usable would turn one connection into unlimited guesses,
// and while guessing an Ed25519 signature is not a practical attack, allowing
// unlimited attempts is not something to leave lying around on purpose.
func (c *Challenge) Verify(pub ed25519.PublicKey, proof []byte) error {
	c.mu.Lock()
	if c.spent {
		c.mu.Unlock()
		return ErrSpent
	}
	c.spent = true
	c.mu.Unlock()

	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("connauth: public key is %d bytes, want %d",
			len(pub), ed25519.PublicKeySize)
	}
	if len(proof) != ed25519.SignatureSize {
		return ErrBadSignature
	}
	if !ed25519.Verify(pub, Message(c.nonce), proof) {
		return ErrBadSignature
	}
	return nil
}
