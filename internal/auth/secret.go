package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

// KeySize is the length of the key that seals secrets such as TOTP keys,
// QUICKSTACK_SECRET_KEY.
const KeySize = 32

var errUnsealed = errors.New("auth: sealed secret is damaged or was sealed for something else")

// sealer encrypts secrets that the database or a form holds, with AES-GCM.
// aad names what a secret is for, such as whose TOTP key it is, so a sealed
// value copied elsewhere doesn't open.
type sealer struct{ aead cipher.AEAD }

func newSealer(key []byte) sealer {
	block, err := aes.NewCipher(key)
	if err != nil || len(key) != KeySize {
		panic("auth: the secret key must be 32 bytes")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return sealer{aead: aead}
}

func (s sealer) seal(plaintext, aad []byte) []byte {
	nonce := make([]byte, s.aead.NonceSize(), s.aead.NonceSize()+len(plaintext)+s.aead.Overhead())
	rand.Read(nonce)
	return s.aead.Seal(nonce, nonce, plaintext, aad)
}

func (s sealer) open(sealed, aad []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, errUnsealed
	}
	out, err := s.aead.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, errUnsealed
	}
	return out, nil
}
