package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"outlookimapservice/pb"
)

const selectMailbox = `
	SELECT id, email, password, refresh_token, access_token, status,
		last_error, is_primary, primary_email, created_at, updated_at
	FROM mailboxes
`

type mailboxRow struct {
	ID           string
	Email        string
	Password     string
	RefreshToken string
	AccessToken  string
	Status       string
	LastError    string
	IsPrimary    bool
	PrimaryEmail string
	CreatedAt    int64
	UpdatedAt    int64
}

type rowScanner interface {
	Scan(dest ...any) error
}

type MailboxStore struct {
	pool             *pgxpool.Pool
	aliasTokenLength int
}

func NewMailboxStore(ctx context.Context, dsn string, aliasTokenLength int) (*MailboxStore, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("PG_DSN is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	store := &MailboxStore{
		pool:             pool,
		aliasTokenLength: aliasTokenLength,
	}
	if store.aliasTokenLength <= 0 {
		store.aliasTokenLength = defaultAliasTokenLength
	}
	if err := store.ensureSchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *MailboxStore) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *MailboxStore) ensureSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS mailboxes (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			password TEXT NOT NULL DEFAULT '',
			refresh_token TEXT NOT NULL DEFAULT '',
			access_token TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'AVAILABLE',
			last_error TEXT NOT NULL DEFAULT '',
			is_primary BOOLEAN NOT NULL DEFAULT false,
			primary_email TEXT NOT NULL DEFAULT '',
			created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
			updated_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT
		)`,
		`ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS password TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS refresh_token TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS access_token TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'AVAILABLE'`,
		`ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS is_primary BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS primary_email TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS created_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT`,
		`ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS updated_at BIGINT NOT NULL DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT`,
		`DROP INDEX IF EXISTS idx_mailboxes_assigned_account`,
		`ALTER TABLE mailboxes DROP COLUMN IF EXISTS assigned_account_id`,
		`CREATE INDEX IF NOT EXISTS idx_mailboxes_status ON mailboxes(status)`,
		`CREATE INDEX IF NOT EXISTS idx_mailboxes_primary ON mailboxes(primary_email)`,
		`UPDATE mailboxes SET status = 'OAUTH_PENDING', last_error = ''
			WHERE status = 'AUTH_FAILED'
			AND last_error = 'registered mailbox has no OAuth refresh token'`,
	}
	for _, statement := range statements {
		if _, err := s.pool.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *MailboxStore) UpsertMailbox(ctx context.Context, mailbox *pb.EmailMailbox) (*pb.EmailMailbox, error) {
	if mailbox == nil {
		return nil, errors.New("mailbox is required")
	}
	email := normalizeEmail(mailbox.GetEmailAddress())
	if email == "" {
		return nil, errors.New("email_address is required")
	}
	isPrimary := mailbox.GetIsPrimary()
	primaryEmail := normalizeEmail(mailbox.GetPrimaryEmail())
	if primaryEmail == "" {
		if isPrimary {
			primaryEmail = email
		} else {
			primaryEmail = canonicalEmail(email)
		}
	}
	if primaryEmail == email {
		isPrimary = true
	}
	requestedStatus := strings.TrimSpace(mailbox.GetStatus())
	insertStatus := requestedStatus
	if insertStatus == "" {
		insertStatus = statusAvailable
	}
	rowID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()

	_, err = s.pool.Exec(ctx, `
		INSERT INTO mailboxes (
			id, email, password, refresh_token, access_token, status,
			last_error, is_primary, primary_email, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (email) DO UPDATE SET
			password = CASE WHEN EXCLUDED.password <> '' THEN EXCLUDED.password ELSE mailboxes.password END,
			refresh_token = CASE WHEN EXCLUDED.refresh_token <> '' THEN EXCLUDED.refresh_token ELSE mailboxes.refresh_token END,
			access_token = CASE WHEN EXCLUDED.access_token <> '' THEN EXCLUDED.access_token ELSE mailboxes.access_token END,
			status = CASE WHEN $12 <> '' THEN EXCLUDED.status ELSE mailboxes.status END,
			last_error = CASE WHEN $12 <> '' OR EXCLUDED.last_error <> '' THEN EXCLUDED.last_error ELSE mailboxes.last_error END,
			is_primary = EXCLUDED.is_primary,
			primary_email = EXCLUDED.primary_email,
			updated_at = EXCLUDED.updated_at
	`, rowID, email, mailbox.GetPassword(), strings.TrimSpace(mailbox.GetRefreshToken()), strings.TrimSpace(mailbox.GetAccessToken()), insertStatus, strings.TrimSpace(mailbox.GetLastError()), isPrimary, primaryEmail, now, now, requestedStatus)
	if err != nil {
		return nil, err
	}
	return s.FindMailbox(ctx, email)
}

func (s *MailboxStore) ListMailboxes(ctx context.Context, status string, limit int32) ([]*pb.EmailMailbox, error) {
	n := int(limit)
	if n <= 0 {
		n = 100
	}
	if n > 500 {
		n = 500
	}
	args := []any{}
	query := selectMailbox + " WHERE 1=1"
	if trimmed := strings.TrimSpace(status); trimmed != "" {
		args = append(args, trimmed)
		query += fmt.Sprintf(" AND status = $%d", len(args))
	}
	args = append(args, n)
	query += fmt.Sprintf(" ORDER BY updated_at DESC LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*pb.EmailMailbox{}
	for rows.Next() {
		row, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row.toProto())
	}
	return out, rows.Err()
}

func (s *MailboxStore) AcquireEmail(ctx context.Context, excludes []string) (*pb.EmailMailbox, error) {
	excludeSet := []string{}
	for _, item := range excludes {
		if normalized := normalizeEmail(item); normalized != "" {
			excludeSet = append(excludeSet, normalized)
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if row, ok, err := s.acquireAvailablePrimary(ctx, tx, excludeSet); err != nil {
		return nil, err
	} else if ok {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return row.toProto(), nil
	}
	if row, ok, err := s.acquireAvailableAlias(ctx, tx, excludeSet); err != nil {
		return nil, err
	} else if ok {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return row.toProto(), nil
	}
	if row, ok, err := s.createAssignedAlias(ctx, tx, excludeSet); err != nil {
		return nil, err
	} else if ok {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return row.toProto(), nil
	}
	return nil, errors.New("no available mailbox")
}

func (s *MailboxStore) acquireAvailablePrimary(ctx context.Context, tx pgx.Tx, excludes []string) (*mailboxRow, bool, error) {
	query := selectMailbox + " WHERE status = $1 AND is_primary = true AND refresh_token <> ''"
	args := []any{statusAvailable}
	query, args = appendExcludes(query, args, excludes)
	query += " ORDER BY updated_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED"
	return s.acquireExisting(ctx, tx, query, args)
}

func (s *MailboxStore) acquireAvailableAlias(ctx context.Context, tx pgx.Tx, excludes []string) (*mailboxRow, bool, error) {
	query := selectMailbox + ` WHERE status = $1
		AND is_primary = false
		AND refresh_token <> ''
		AND primary_email IN (
			SELECT email FROM mailboxes
			WHERE is_primary = true AND status = $2 AND refresh_token <> ''
		)`
	args := []any{statusAvailable, statusRegistered}
	query, args = appendExcludes(query, args, excludes)
	query += " ORDER BY updated_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED"
	return s.acquireExisting(ctx, tx, query, args)
}

func (s *MailboxStore) acquireExisting(ctx context.Context, tx pgx.Tx, query string, args []any) (*mailboxRow, bool, error) {
	row, err := scanMailbox(tx.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	now := time.Now().Unix()
	if _, err := tx.Exec(ctx, "UPDATE mailboxes SET status = $1, last_error = '', updated_at = $2 WHERE email = $3", statusAssigned, now, row.Email); err != nil {
		return nil, false, err
	}
	row.Status = statusAssigned
	row.LastError = ""
	row.UpdatedAt = now
	return row, true, nil
}

func (s *MailboxStore) createAssignedAlias(ctx context.Context, tx pgx.Tx, excludes []string) (*mailboxRow, bool, error) {
	primary, err := scanMailbox(tx.QueryRow(ctx, selectMailbox+" WHERE is_primary = true AND status = $1 AND refresh_token <> '' ORDER BY updated_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED", statusRegistered))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	excludeMap := map[string]struct{}{}
	for _, item := range excludes {
		excludeMap[item] = struct{}{}
	}
	for i := 0; i < 20; i++ {
		alias, err := s.makeAlias(primary.Email)
		if err != nil {
			return nil, false, err
		}
		if _, ok := excludeMap[alias]; ok {
			continue
		}
		rowID, err := randomHex(16)
		if err != nil {
			return nil, false, err
		}
		now := time.Now().Unix()
		row := &mailboxRow{
			ID:           rowID,
			Email:        alias,
			Password:     primary.Password,
			RefreshToken: primary.RefreshToken,
			AccessToken:  primary.AccessToken,
			Status:       statusAssigned,
			LastError:    "",
			IsPrimary:    false,
			PrimaryEmail: primary.Email,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO mailboxes (
				id, email, password, refresh_token, access_token, status,
				last_error, is_primary, primary_email, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,'',$7,$8,$9,$10)
			ON CONFLICT (email) DO NOTHING
		`, row.ID, row.Email, row.Password, row.RefreshToken, row.AccessToken, row.Status, row.IsPrimary, row.PrimaryEmail, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, false, err
		}
		if tag.RowsAffected() > 0 {
			return row, true, nil
		}
	}
	return nil, false, fmt.Errorf("failed to create unique alias for %s", redactEmail(primary.Email))
}

func (s *MailboxStore) makeAlias(primary string) (string, error) {
	local, domain, ok := strings.Cut(normalizeEmail(primary), "@")
	if !ok || local == "" || domain == "" {
		return "", fmt.Errorf("invalid primary email: %s", redactEmail(primary))
	}
	token, err := randomAliasToken(s.aliasTokenLength)
	if err != nil {
		return "", err
	}
	return local + "+" + token + "@" + domain, nil
}

func (s *MailboxStore) MarkEmailStatus(ctx context.Context, email string, status string, lastError string) (*pb.EmailMailbox, error) {
	email = normalizeEmail(email)
	status = strings.TrimSpace(status)
	if email == "" {
		return nil, errors.New("email_address is required")
	}
	if status == "" {
		return nil, errors.New("status is required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row, err := scanMailbox(tx.QueryRow(ctx, selectMailbox+" WHERE email = $1 FOR UPDATE", email))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("mailbox not found: %s", redactEmail(email))
	}
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	trimmedError := strings.TrimSpace(lastError)
	if _, err := tx.Exec(ctx, "UPDATE mailboxes SET status = $1, last_error = $2, updated_at = $3 WHERE email = $4", status, trimmedError, now, email); err != nil {
		return nil, err
	}
	if status == statusUserAlreadyExists {
		primary := row.PrimaryEmail
		if primary == "" {
			primary = row.Email
		}
		if _, err := tx.Exec(ctx, "UPDATE mailboxes SET status = $1, last_error = $2, updated_at = $3 WHERE email = $4 AND is_primary = true AND status <> $5", statusBlocked, trimmedError, now, primary, statusUserAlreadyExists); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, "UPDATE mailboxes SET status = $1, last_error = $2, updated_at = $3 WHERE primary_email = $4 AND is_primary = false AND status = $5", statusBlocked, trimmedError, now, primary, statusAvailable); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.FindMailbox(ctx, email)
}

func (s *MailboxStore) FindMailbox(ctx context.Context, email string) (*pb.EmailMailbox, error) {
	row, err := scanMailbox(s.pool.QueryRow(ctx, selectMailbox+" WHERE email = $1", normalizeEmail(email)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("mailbox not found: %s", redactEmail(email))
	}
	if err != nil {
		return nil, err
	}
	return row.toProto(), nil
}

func (s *MailboxStore) PollMailboxForEmail(ctx context.Context, email string) (*pb.EmailMailbox, error) {
	email = normalizeEmail(email)
	row, err := scanMailbox(s.pool.QueryRow(ctx, selectMailbox+" WHERE email = $1", email))
	if errors.Is(err, pgx.ErrNoRows) {
		canonical := canonicalEmail(email)
		if canonical != "" && canonical != email {
			row, err = scanMailbox(s.pool.QueryRow(ctx, selectMailbox+" WHERE email = $1 AND is_primary = true", canonical))
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("mailbox not found: %s", redactEmail(email))
	}
	if err != nil {
		return nil, err
	}

	primaryEmail := row.Email
	if !row.IsPrimary {
		primaryEmail = row.PrimaryEmail
		if primaryEmail == "" {
			primaryEmail = canonicalEmail(row.Email)
		}
	}
	primary, err := scanMailbox(s.pool.QueryRow(ctx, selectMailbox+" WHERE email = $1 AND is_primary = true", primaryEmail))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("primary mailbox not found for %s", redactEmail(email))
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(primary.RefreshToken) == "" {
		return nil, fmt.Errorf("primary mailbox has no refresh token: %s", redactEmail(primary.Email))
	}
	if primary.Status == statusAuthFailed || primary.Status == statusBlocked {
		return nil, fmt.Errorf("primary mailbox is not pollable: %s status=%s", redactEmail(primary.Email), primary.Status)
	}
	return primary.toProto(), nil
}

func (s *MailboxStore) UpdateMailboxTokens(ctx context.Context, email string, refreshToken string, accessToken string) error {
	_, err := s.pool.Exec(ctx, "UPDATE mailboxes SET refresh_token = $1, access_token = $2, updated_at = $3 WHERE email = $4", strings.TrimSpace(refreshToken), strings.TrimSpace(accessToken), time.Now().Unix(), normalizeEmail(email))
	return err
}

func (s *MailboxStore) MarkAuthFailed(ctx context.Context, email string, err error) {
	if _, updateErr := s.MarkEmailStatus(ctx, email, statusAuthFailed, err.Error()); updateErr != nil {
		logWarning("failed to mark mailbox auth failed for %s: %v", redactEmail(email), updateErr)
	}
}

func appendExcludes(query string, args []any, excludes []string) (string, []any) {
	if len(excludes) == 0 {
		return query, args
	}
	placeholders := make([]string, 0, len(excludes))
	for _, exclude := range excludes {
		args = append(args, exclude)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	query += " AND email NOT IN (" + strings.Join(placeholders, ",") + ")"
	return query, args
}

func scanMailbox(scanner rowScanner) (*mailboxRow, error) {
	var row mailboxRow
	err := scanner.Scan(
		&row.ID,
		&row.Email,
		&row.Password,
		&row.RefreshToken,
		&row.AccessToken,
		&row.Status,
		&row.LastError,
		&row.IsPrimary,
		&row.PrimaryEmail,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (m *mailboxRow) toProto() *pb.EmailMailbox {
	if m == nil {
		return nil
	}
	return &pb.EmailMailbox{
		EmailAddress: m.Email,
		Password:     m.Password,
		RefreshToken: m.RefreshToken,
		AccessToken:  m.AccessToken,
		Status:       m.Status,
		LastError:    m.LastError,
		IsPrimary:    m.IsPrimary,
		PrimaryEmail: m.PrimaryEmail,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}
