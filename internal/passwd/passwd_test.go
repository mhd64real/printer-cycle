package passwd_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mhd64real/printer-cycle/internal/passwd"
)

func TestHashAndVerify(t *testing.T) {
	const pw = "correct horse battery staple"

	encoded, err := passwd.Hash(pw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$") {
		t.Fatalf("hash = %q, want a PHC argon2id string", encoded)
	}

	ok, err := passwd.Verify(pw, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("the correct password did not verify")
	}

	ok, err = passwd.Verify(pw+" ", encoded)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a password differing by one character verified")
	}
}

// Two hashes of the same password must differ, or the salt is not doing its job
// and identical passwords would be visibly identical in the database.
func TestHashesAreSalted(t *testing.T) {
	a, err := passwd.Hash("same")
	if err != nil {
		t.Fatal(err)
	}
	b, err := passwd.Hash("same")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two hashes of the same password are identical, so it is unsalted")
	}
}

// Parameters are read from the stored hash, not from the constants in this
// package, so raising the cost later must not lock anyone out.
func TestVerifyHonoursStoredParameters(t *testing.T) {
	encoded, err := passwd.Hash("legacy")
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite the parameter section to something else and confirm Verify reads
	// it rather than assuming. A mismatch must fail rather than silently pass.
	bad := strings.Replace(encoded, "t=2", "t=3", 1)
	ok, err := passwd.Verify("legacy", bad)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("verification succeeded with different parameters, so the stored ones are being ignored")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	for _, bad := range []string{
		"",
		"not a hash",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdA",
		"$argon2id$badversion$m=19456,t=2,p=1$c2FsdA$aGFzaA",
	} {
		if _, err := passwd.Verify("x", bad); err == nil {
			t.Errorf("Verify accepted a malformed hash: %q", bad)
		}
	}
}

// Records what a sign-in costs. Not an assertion: the number depends on the
// machine. It exists because the parameters were chosen for a Pi Zero 2 W,
// which is roughly an order of magnitude slower than anything this runs on
// during development.
func TestHashCost(t *testing.T) {
	start := time.Now()
	const n = 5
	for range n {
		if _, err := passwd.Hash("measure me"); err != nil {
			t.Fatal(err)
		}
	}
	per := time.Since(start) / n

	t.Logf("argon2id at 19MiB, t=2, p=1: %v per hash on this machine", per.Round(time.Millisecond))
	t.Logf("a Pi Zero 2 W core is perhaps 10 to 20 times slower, so expect roughly %v to %v there",
		(per * 10).Round(10*time.Millisecond), (per * 20).Round(10*time.Millisecond))
}
