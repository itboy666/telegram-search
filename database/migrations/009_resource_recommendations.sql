CREATE TABLE IF NOT EXISTS ts_resource_likes (
 user_id BIGINT UNSIGNED NOT NULL,
 resource_id BIGINT UNSIGNED NOT NULL,
 created_at DATETIME NOT NULL,
 PRIMARY KEY(user_id,resource_id),
 KEY idx_resource(resource_id,user_id),
 CONSTRAINT fk_resource_like_user FOREIGN KEY(user_id) REFERENCES ts_users(id) ON DELETE CASCADE,
 CONSTRAINT fk_resource_like_resource FOREIGN KEY(resource_id) REFERENCES ts_resources(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ts_search_snapshots (
 token_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
 query_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 candidate_ids JSON NOT NULL,
 total BIGINT UNSIGNED NOT NULL,
 window_total INT UNSIGNED NOT NULL,
 page_size TINYINT UNSIGNED NOT NULL,
 created_at DATETIME NOT NULL,
 expires_at DATETIME NOT NULL,
 KEY idx_expiry(expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
