package conn

import (
	"crypto/rand"
	"fmt"
)

// NewID returns the identifier a saved connection is filed under.
//
// It is random, and it is the one thing about a connection that never changes.
// The password in the keychain is addressed by it, which is why the obvious
// alternatives are refused: keying the secret on the name loses it the moment
// someone renames the connection and collides when two connections share a
// name, and keying it on user@host:port/database orphans it on any legitimate
// edit of the host or the user.
//
// The shape is a version 4 UUID because that is what someone opening the
// connections file — or the keychain viewer of their system — recognises as an
// identifier rather than mistakes for something they are meant to edit.
func NewID() string {
	var raw [16]byte
	// Documented to fill the slice entirely or panic, so there is no failure
	// here to handle: an identifier that is only partly random would be worse
	// than no identifier at all, and this cannot produce one.
	_, _ = rand.Read(raw[:])

	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}
