package postgres

import "ride-home-router/internal/credentials"

func (s *Store) ConfigureCredentialEncryption(encodedKey string) error {
	c, err := credentials.New(encodedKey)
	if err != nil {
		return err
	}
	s.credentialCipher = c
	return nil
}
