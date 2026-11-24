package app

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/go-sql-driver/mysql"
)

type databaseUpgradeStatus struct {
	CurrentVersion  string   `json:"current_version"`
	TargetVersion   string   `json:"target_version"`
	PendingVersions []string `json:"pending_versions"`
	UpToDate        bool     `json:"up_to_date"`
	Running         bool     `json:"running"`
}

func readDatabaseUpgradeStatus(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (databaseUpgradeStatus, error) {
	status := databaseUpgradeStatus{
		TargetVersion:   migrationVersions[len(migrationVersions)-1],
		PendingVersions: []string{},
	}
	applied := make(map[string]bool)
	rows, err := db.QueryContext(ctx, "SELECT version FROM ts_schema_migrations")
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1146 {
			return status, err
		}
	} else {
		defer rows.Close()
		for rows.Next() {
			var version string
			if err := rows.Scan(&version); err != nil {
				return status, err
			}
			applied[version] = true
		}
		if err := rows.Err(); err != nil {
			return status, err
		}
	}
	for _, version := range migrationVersions {
		if applied[version] {
			status.CurrentVersion = version
		} else {
			status.PendingVersions = append(status.PendingVersions, version)
		}
	}
	status.UpToDate = len(status.PendingVersions) == 0
	return status, nil
}

func (a *App) registerDatabaseUpgrade() {
	// AccessFor grants access.manage only to super administrators.
	a.Handle("GET /admin/api/database-upgrade", "admin", "access.manage", false, func(w http.ResponseWriter, r *http.Request, _ *Identity) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var free bool
		if err := a.DB.QueryRowContext(ctx, "SELECT IS_FREE_LOCK(?)", migrationLockName).Scan(&free); err != nil {
			Error(w, err)
			return
		}
		status, err := readDatabaseUpgradeStatus(ctx, a.DB)
		if err != nil {
			Error(w, err)
			return
		}
		status.Running = !free
		JSON(w, status)
	})
	a.Handle("POST /admin/api/database-upgrade", "admin", "access.manage", true, func(w http.ResponseWriter, r *http.Request, id *Identity) {
		body, err := Body(r)
		if err != nil {
			Error(w, err)
			return
		}
		if len(body) > 0 {
			Fail(w, http.StatusUnprocessableEntity, "数据库升级不接受额外参数。")
			return
		}
		// Keep an accepted migration running if the browser closes or its proxy times out.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
		defer cancel()
		var status databaseUpgradeStatus
		err = a.withMigrationLock(ctx, 0, func(conn *sql.Conn) error {
			status, err = readDatabaseUpgradeStatus(ctx, conn)
			if err != nil || status.UpToDate {
				return err
			}
			if err = a.applyMigrations(ctx, conn, id.ID); err != nil {
				return err
			}
			status, err = readDatabaseUpgradeStatus(ctx, conn)
			return err
		})
		switch {
		case err == nil:
			JSON(w, status)
		case errors.Is(err, errMigrationBusy), errors.Is(err, errLegacyCatalog):
			Fail(w, http.StatusConflict, err.Error())
		default:
			log.Printf("admin database upgrade failed (%T)", err)
			Fail(w, http.StatusInternalServerError, "数据库升级未完成，请检查部署文件和数据库权限后重试。")
		}
	})
}
