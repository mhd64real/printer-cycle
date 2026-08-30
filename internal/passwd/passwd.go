// Package passwd hashes and verifies user passwords.
//
// Argon2id, in the PHC string format, so the parameters travel with the hash
// and can be raised later without invalidating anything already stored.
package passwd

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Parameters, chosen for a Raspberry Pi Zero 2 W rather than for a server.
//
// RFC 9106 offers two profiles. The first asks for 2 GiB of memory, and the
// second, intended for constrained machines, asks for 64 MiB. Even 64 MiB is a
// poor idea here: this box has 512 MiB in total, shared with cupsd and with
// Ghostscript, which spikes hard while rasterising. Several people signing in
// at once must not be able to push the machine into an out-of-memory kill.
//
// So this uses the RFC's low-memory option: 19 MiB, two passes, one lane. That
// is a deliberate trade. On a machine with room, more memory would be better.
// On this one, a password hash that can crash the print server is worse than a
// slightly cheaper one.
const (
	memoryKiB = 19 * 1024
	time_     = 2
	parallel  = 1
	saltLen   = 16
	keyLen    = 32
)

var (
	ErrInvalidHash = errors.New("passwd: hash is not a recognisable argon2id string")
	ErrWrongParams = errors.New("passwd: hash uses parameters this build cannot read")
)

// Hash produces a PHC-format argon2id string for password.
func Hash(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("passwd: reading random salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, time_, memoryKiB, parallel, keyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memoryKiB, time_, parallel,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify reports whether password matches encoded.
//
// The parameters come from the stored string rather than from the constants
// above, so raising the cost later does not lock anybody out of an account
// created before the change.
func Verify(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, ErrInvalidHash
	}
	if version != argon2.Version {
		return false, ErrWrongParams
	}

	var memory uint32
	var times, lanes uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &times, &lanes); err != nil {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHash
	}

	got := argon2.IDKey([]byte(password), salt, uint32(times), memory, lanes, uint32(len(want)))

	// Constant time, so the comparison cannot leak how much of the hash matched.
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// Dummy is a hash of nothing in particular, used to spend the same work on a
// username that does not exist as on one that does.
//
// Without it, a failed sign-in for an unknown user returns almost instantly
// while a wrong password for a real user takes a hundred milliseconds or more,
// and that difference is enough to enumerate who has an account on the box.
var Dummy, _ = Hash("printer-cycle-not-a-real-password")
