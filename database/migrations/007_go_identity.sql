CREATE TABLE IF NOT EXISTS ts_telegram_auth_challenges (
 id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
 browser_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 bot_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
 purpose VARCHAR(12) NOT NULL,
 user_id BIGINT UNSIGNED NULL,
 session_id BIGINT UNSIGNED NULL,
 bot_id BIGINT UNSIGNED NOT NULL,
 telegram_id BIGINT UNSIGNED NULL,
 telegram_profile JSON NULL,
 status VARCHAR(16) NOT NULL DEFAULT 'pending',
 created_at DATETIME NOT NULL,
 expires_at DATETIME NOT NULL,
 confirmed_at DATETIME NULL,
 consumed_at DATETIME NULL,
 KEY ix_auth_expiry(expires_at),
 KEY ix_auth_user(user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS ts_telegram_profiles (
 user_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 telegram_id BIGINT UNSIGNED NOT NULL UNIQUE,
 username VARCHAR(80) NOT NULL DEFAULT '',
 display_name VARCHAR(100) NOT NULL DEFAULT '',
 updated_at DATETIME NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS ts_go_worker_state (
 name VARCHAR(80) PRIMARY KEY,
 payload JSON NOT NULL,
 updated_at DATETIME NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
