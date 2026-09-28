// Package gen produces random proxy-user credentials. Alphabets carry no
// look-alike characters and no specials: the values land in a 3proxy CL
// users line, in URLs shown to customers, and are typed by humans.
package gen

import (
	"crypto/rand"
	"math/big"
)

const (
	usernameAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	passwordAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
)

// String returns n random characters from alphabet.
func String(alphabet string, n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err) // crypto/rand failure is unrecoverable
		}
		b[i] = alphabet[idx.Int64()]
	}
	return string(b)
}

// Username returns a random proxy username ("p" + 8 chars).
func Username() string { return "p" + String(usernameAlphabet, 8) }

// Password returns a random proxy password (12 chars).
func Password() string { return String(passwordAlphabet, 12) }
