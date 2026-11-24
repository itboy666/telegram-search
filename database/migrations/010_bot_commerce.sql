CREATE TABLE IF NOT EXISTS ts_bot_commerce_sessions (
 id CHAR(24) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 bot_id BIGINT UNSIGNED NOT NULL,
 telegram_user_id BIGINT UNSIGNED NOT NULL,
 chat_id BIGINT NOT NULL,
 user_id BIGINT UNSIGNED NOT NULL,
 state VARCHAR(32) NOT NULL DEFAULT 'home',
 payload JSON NOT NULL,
 revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
 message_id BIGINT NOT NULL DEFAULT 0,
 last_input_id BIGINT NOT NULL DEFAULT 0,
 expires_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 UNIQUE KEY uq_bot_commerce_user(bot_id,telegram_user_id),
 KEY ix_bot_commerce_expiry(expires_at),
 KEY ix_bot_commerce_account(user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
