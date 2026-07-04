package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SettingsStore is a small per-tenant key/value store (JSONB values).
// It backs hook_settings, replacing the upstream local config file.
type SettingsStore struct {
	pool   *pgxpool.Pool
	schema string
}

// NewSettingsStore returns a SettingsStore for the given tenant.
func NewSettingsStore(pool *pgxpool.Pool, tenantID string) *SettingsStore {
	return &SettingsStore{pool: pool, schema: SafeSchemaName(tenantID)}
}

// ProvisionSettings creates the settings table for a tenant (idempotent).
func ProvisionSettings(ctx context.Context, pool *pgxpool.Pool, tenantID string) error {
	schema := SafeSchemaName(tenantID)
	stmts := []string{
		fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, schema),
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.settings (
				key   TEXT PRIMARY KEY,
				value JSONB NOT NULL
			)`, schema),
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("provision settings %s: %w", schema, err)
		}
	}
	return nil
}

// Get returns the stored value for key, or (nil, nil) if absent.
func (s *SettingsStore) Get(ctx context.Context, key string) (any, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, fmt.Sprintf(`SELECT value FROM %s.settings WHERE key = $1`, s.schema), key).Scan(&raw)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// Set upserts a key/value pair.
func (s *SettingsStore) Set(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, s.schema), key, raw)
	return err
}

// GetBool returns a stored boolean, or def if missing/non-boolean.
func (s *SettingsStore) GetBool(ctx context.Context, key string, def bool) (bool, error) {
	v, err := s.Get(ctx, key)
	if err != nil {
		return def, err
	}
	if b, ok := v.(bool); ok {
		return b, nil
	}
	return def, nil
}
