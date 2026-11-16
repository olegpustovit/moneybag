-- +goose Up
-- Keep users unchanged: the legacy binary decodes SELECT * into a strict struct.
CREATE TABLE user_identities (
    subject text PRIMARY KEY NOT NULL DEFAULT '' CHECK (length(subject) BETWEEN 1 AND 255),
    user_id text NOT NULL DEFAULT '' UNIQUE REFERENCES users(id) ON DELETE CASCADE
);
