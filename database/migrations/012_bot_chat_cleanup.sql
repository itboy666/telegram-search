CREATE TABLE IF NOT EXISTS ts_bot_chat_views (
 bot_id BIGINT UNSIGNED NOT NULL,
 chat_id BIGINT NOT NULL,
 view_key VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 message_id BIGINT NOT NULL,
 sent_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 PRIMARY KEY(bot_id,chat_id,view_key),
 KEY ix_bot_chat_view_age(updated_at),
 KEY ix_bot_chat_view_message(bot_id,chat_id,message_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ts_bot_message_cleanup (
 bot_id BIGINT UNSIGNED NOT NULL,
 chat_id BIGINT NOT NULL,
 message_id BIGINT NOT NULL,
 attempts TINYINT UNSIGNED NOT NULL DEFAULT 0,
 next_attempt_at DATETIME NOT NULL,
 expires_at DATETIME NOT NULL,
 PRIMARY KEY(bot_id,chat_id,message_id),
 KEY ix_bot_cleanup_due(bot_id,next_attempt_at),
 KEY ix_bot_cleanup_expiry(expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
