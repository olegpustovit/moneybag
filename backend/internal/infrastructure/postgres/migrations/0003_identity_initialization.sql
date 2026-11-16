-- +goose Up
-- Existing linked accounts keep their models; only new ID accounts need defaults.
ALTER TABLE user_identities ADD COLUMN needs_initialization boolean NOT NULL DEFAULT false;
