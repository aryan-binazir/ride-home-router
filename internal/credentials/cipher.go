package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"strings"
)

const prefix = "v1:"

type Cipher struct{ aead cipher.AEAD }

func New(encodedKey string) (*Cipher, error) {
	key, err := base64.StdEncoding.Strict().DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("credential encryption key must be a base64-encoded 32-byte key")
	}
	block, err := aes.NewCipher(key)
	clear(key)
	if err != nil {
		return nil, errors.New("could not initialize credential encryption")
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, errors.New("could not initialize credential encryption")
	}
	return &Cipher{aead: aead}, nil
}

// Seal authenticates the purpose as well as the ciphertext, preventing cross-credential substitution.
func (c *Cipher) Seal(plaintext, purpose string) (string, error) {
	if c == nil {
		return "", errors.New("credential encryption is not configured")
	}
	return prefix + base64.StdEncoding.EncodeToString(c.aead.Seal(nil, nil, []byte(plaintext), []byte(purpose))), nil
}

func (c *Cipher) Open(envelope, purpose string) (string, error) {
	if c == nil {
		return "", errors.New("credential encryption is not configured")
	}
	if !strings.HasPrefix(envelope, prefix) {
		return "", errors.New("unsupported encrypted credential format")
	}
	ciphertext, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(envelope, prefix))
	if err != nil {
		return "", errors.New("encrypted credential is invalid")
	}
	plaintext, err := c.aead.Open(nil, nil, ciphertext, []byte(purpose))
	if err != nil {
		return "", errors.New("credential could not be decrypted; check the encryption key")
	}
	defer clear(plaintext)
	return string(plaintext), nil
}
