package postgres

import "ride-home-router/internal/credentials"

// ConfigureCredentialEncryption must run once before the store serves requests.
func (s *Store) ConfigureCredentialEncryption(encodedKey string) error {
	c, err := credentials.New(encodedKey)
	if err != nil {
		return err
	}
	s.credentialCipher = c
	return nil
}
