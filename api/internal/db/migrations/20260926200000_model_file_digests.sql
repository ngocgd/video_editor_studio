-- +goose Up
-- Small non-LFS config files (tokenizers, model configs) have no sha256
-- published by the Hub, so the manifest pins them by their git blob id
-- at the pinned revision instead. model_files now stores either pin as a
-- digest string: a sha256 hex, or "git-sha1:<hex>".
ALTER TABLE model_files RENAME COLUMN sha256 TO digest;
ALTER TABLE model_files DROP CONSTRAINT model_files_sha256_check;
ALTER TABLE model_files ADD CONSTRAINT model_files_digest_check
    CHECK (digest ~ '^[0-9a-f]{64}$' OR digest ~ '^git-sha1:[0-9a-f]{40}$');

-- +goose Down
DELETE FROM model_files WHERE digest LIKE 'git-sha1:%';
ALTER TABLE model_files DROP CONSTRAINT model_files_digest_check;
ALTER TABLE model_files RENAME COLUMN digest TO sha256;
ALTER TABLE model_files ADD CONSTRAINT model_files_sha256_check CHECK (sha256 ~ '^[0-9a-f]{64}$');
