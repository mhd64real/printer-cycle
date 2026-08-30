package store

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

// crockford is Crockford's base32 alphabet: no I, L, O or U, so an identifier
// read aloud or typed by hand cannot be confused between one and I, or zero and
// O. Identifiers show up in support conversations, and this costs nothing.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewID returns a sortable identifier with the given prefix, such as
// "user_01K7Q3JXBM9F4T2N8YV6CDR5AE".
//
// The first ten characters encode a millisecond timestamp and the remaining
// sixteen are random, which is the ULID layout. Sorting by id therefore sorts by
// creation time, so listings come out in a sensible order without an index on a
// timestamp column, and identifiers generated on the same millisecond still do
// not collide.
func NewID(prefix string) string {
	var buf [16]byte

	ms := uint64(time.Now().UnixMilli())
	binary.BigEndian.PutUint64(buf[:8], ms<<16)
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand does not fail on any platform this runs on, and a
		// half-random identifier would be worse than stopping.
		panic("store: crypto/rand unavailable: " + err.Error())
	}

	out := make([]byte, 26)
	// 26 base32 characters carry 130 bits; the top two are always zero.
	var bits, value uint32
	j := 25
	for i := 15; i >= 0; i-- {
		value |= uint32(buf[i]) << bits
		bits += 8
		for bits >= 5 {
			out[j] = crockford[value&31]
			j--
			value >>= 5
			bits -= 5
		}
	}
	for j >= 0 {
		out[j] = crockford[value&31]
		value >>= 5
		j--
	}

	return prefix + "_" + string(out)
}
