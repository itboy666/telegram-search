package app

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const migrationLockName = "starindex_go_migration"

var (
	versionedMigrationFiles = []string{"005_bot_security.sql", "006_commerce.sql", "007_go_identity.sql", "008_group_management.sql", "009_resource_recommendations.sql", "010_bot_commerce.sql", "011_commerce_proofs.sql", "012_bot_chat_cleanup.sql", "013_installation.sql"}
	migrationFiles          = append([]string{"001_initial.sql", "002_admin_rbac.sql"}, versionedMigrationFiles...)
	migrationVersions       = func() []string {
		versions := []string{"003_bot_search", "004_catalog_identity"}
		for _, file := range versionedMigrationFiles {
			versions = append(versions, strings.TrimSuffix(file, ".sql"))
		}
		return versions
	}()
	errMigrationBusy = errors.New("另一个数据库迁移正在运行。")
	errLegacyCatalog = errors.New("旧版收录表尚未完成链接去重迁移，请先运行原版 app:upgrade-catalog，再迁移 Go。")
)

// Migrate is additive on an existing installation. It never recreates business
// tables, resets credentials, changes balances, or removes the PHP fallback.
func (a *App) Migrate(ctx context.Context) error {
	return a.migrate(ctx, true)
}

func (a *App) migrate(ctx context.Context, createAdmin bool) error {
	return a.withMigrationLock(ctx, 10, func(conn *sql.Conn) error {
		if err := a.applyMigrations(ctx, conn, 0); err != nil {
			return err
		}
		if createAdmin {
			return a.ensureMigrationAdmin(ctx, conn)
		}
		return nil
	})
}

func (a *App) withMigrationLock(ctx context.Context, waitSeconds int, run func(*sql.Conn) error) error {
	conn, e := a.DB.Conn(ctx)
	if e != nil {
		return e
	}
	defer conn.Close()
	var locked int
	if e = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,?)", migrationLockName, waitSeconds).Scan(&locked); e != nil {
		return e
	}
	if locked != 1 {
		return errMigrationBusy
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var released int
		if err := conn.QueryRowContext(release, "SELECT RELEASE_LOCK(?)", migrationLockName).Scan(&released); err != nil || released != 1 {
			// A connection with an unresolved advisory lock cannot return to the pool.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	return run(conn)
}

func (a *App) applyMigrations(ctx context.Context, conn *sql.Conn, actor int64) error {
	scripts := make([]string, 0, len(migrationFiles))
	for _, file := range migrationFiles {
		b, e := os.ReadFile(filepath.Join(a.Root, "database/migrations", file))
		if e != nil {
			return e
		}
		scripts = append(scripts, string(b))
	}
	for i, script := range scripts {
		for _, stmt := range strings.Split(script, ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("migration %s failed: %w", migrationFiles[i], err)
			}
		}
	}
	columns := map[string]map[string]string{
		"ts_users":        {"submission_allowed": "TINYINT UNSIGNED NOT NULL DEFAULT 1", "submission_note": "VARCHAR(500) NOT NULL DEFAULT ''"},
		"ts_resources":    {"status_reason": "VARCHAR(500) NOT NULL DEFAULT ''", "like_count": "BIGINT UNSIGNED NOT NULL DEFAULT 0"},
		"ts_bot_sessions": {"page_size": "TINYINT UNSIGNED NOT NULL DEFAULT 5", "filters_expanded": "TINYINT UNSIGNED NOT NULL DEFAULT 1", "search_token": "VARCHAR(64) NOT NULL DEFAULT ''"},
		"ts_bot_groups": {
			"message_max_chars":    "INT UNSIGNED NOT NULL DEFAULT 0",
			"block_media":          "TINYINT UNSIGNED NOT NULL DEFAULT 0",
			"block_forwards":       "TINYINT UNSIGNED NOT NULL DEFAULT 0",
			"join_verify_mode":     "VARCHAR(16) NOT NULL DEFAULT 'off'",
			"join_timeout_seconds": "INT UNSIGNED NOT NULL DEFAULT 180",
			"join_max_attempts":    "TINYINT UNSIGNED NOT NULL DEFAULT 3",
			"join_failure_action":  "VARCHAR(8) NOT NULL DEFAULT 'mute'",
			"welcome_enabled":      "TINYINT UNSIGNED NOT NULL DEFAULT 0",
			"welcome_template":     "VARCHAR(2000) NOT NULL DEFAULT ''",
			"can_restrict":         "TINYINT UNSIGNED NOT NULL DEFAULT 0",
		},
	}
	for table, defs := range columns {
		for name, def := range defs {
			var count int
			if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?", table, name).Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				if _, err := conn.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+name+" "+def); err != nil {
					return err
				}
			}
		}
	}
	var urlColumn int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ts_submissions' AND column_name='url_hash'").Scan(&urlColumn); err != nil {
		return err
	}
	if urlColumn == 0 {
		var hasSubmissions bool
		if err := conn.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM ts_submissions)").Scan(&hasSubmissions); err != nil {
			return err
		}
		if hasSubmissions {
			return errLegacyCatalog
		}
		if _, err := conn.ExecContext(ctx, "ALTER TABLE ts_submissions ADD COLUMN url_hash CHAR(64) NOT NULL, ADD UNIQUE KEY uq_submission_url(url_hash)"); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS ts_link_locks(url_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY) ENGINE=InnoDB"); err != nil {
		return err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO ts_commerce_config(id,config,updated_at) VALUES(1,'{}',?)", now()); err != nil {
		return err
	}
	var hasSlots bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM ts_ad_slots)").Scan(&hasSlots); err != nil {
		return err
	}
	if !hasSlots {
		for kind, label := range adKinds {
			if _, err = tx.ExecContext(ctx, "INSERT INTO ts_ad_slots(name,kind,description,price,duration_days,capacity,enabled,created_at,updated_at) VALUES(?,?,?,10000000,7,1,0,?,?)", label, kind, "配置价格和目标后开放购买。", now(), now()); err != nil {
				return err
			}
		}
	}
	for _, version := range migrationVersions {
		if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO ts_schema_migrations(version,applied_at) VALUES(?,?)", version, now()); err != nil {
			return err
		}
	}
	if actor != 0 {
		target := migrationVersions[len(migrationVersions)-1]
		if err = catalogAudit(tx, actor, "升级数据库", target); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *App) ensureMigrationAdmin(ctx context.Context, conn *sql.Conn) error {
	var admins int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM ts_admins").Scan(&admins); err != nil {
		return err
	}
	if admins == 0 {
		// Fresh installs only; existing users and administrators are never reset.
		pass := randomHex(16)
		pw, e := passwordHash(pass)
		if e != nil {
			return e
		}
		if _, e = conn.ExecContext(ctx, "INSERT INTO ts_admins(username,display_name,password_hash,is_super,status,created_at) VALUES('admin','系统管理员',?,1,1,?)", pw, now()); e != nil {
			return e
		}
		f, e := os.OpenFile(filepath.Join(a.Root, "本地登录信息-Go.txt"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return errors.New("管理员已创建，但初始登录文件无法写入，请使用受控密码重置流程")
		}
		_, e = f.WriteString("后台账号：admin\n初始密码：" + pass + "\n生成时间：" + time.Now().In(location).Format(time.RFC3339) + "\n")
		f.Close()
		if e != nil {
			return e
		}
	}
	return nil
}
