package postgres

import "context"

func (s *Store) Approved(ctx context.Context, emails []string) (bool, error) {
	var approved bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM approved_emails WHERE email = ANY($1::text[]))`, emails).Scan(&approved)
	return approved, err
}

func (s *Store) ApprovedEmails(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT email FROM approved_emails ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	emails := []string{}
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		emails = append(emails, email)
	}
	return emails, rows.Err()
}

func (s *Store) AddApprovedEmail(ctx context.Context, email string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO approved_emails(email) VALUES ($1) ON CONFLICT DO NOTHING`, email)
	return err
}

func (s *Store) RemoveApprovedEmail(ctx context.Context, email string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM approved_emails WHERE email=$1`, email)
	return err
}

// RecordAdminEmails retains first verification only, never an access grant.
func (s *Store) RecordAdminEmails(ctx context.Context, emails []string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO verified_admin_emails(email) SELECT DISTINCT unnest($1::text[]) ON CONFLICT DO NOTHING`, emails)
	return err
}
