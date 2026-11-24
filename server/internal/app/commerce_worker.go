package app

import (
	"context"
	"errors"
	"log"
	"time"
)

// RunCommerce uses a dedicated MySQL connection for the advisory lock.
// Every funds mutation is additionally protected by transactions and unique keys.
func (a *App) RunCommerce(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := a.CommerceOnce(ctx); err != nil && ctx.Err() == nil {
			log.Print("commerce: 本轮任务未完成，订单已保留，将在下轮重试")
		}
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (a *App) CommerceOnce(ctx context.Context) error {
	connection, err := a.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	var acquired int
	if err = connection.QueryRowContext(ctx, "SELECT GET_LOCK('starindex_commerce_worker',0)").Scan(&acquired); err != nil {
		return err
	}
	if acquired != 1 {
		return nil
	}
	defer connection.ExecContext(context.Background(), "SELECT RELEASE_LOCK('starindex_commerce_worker')")
	// Keep one bounded cleanup batch per tick so a backlog cannot monopolize
	// payment processing. The explicit maintain command handles larger backlogs.
	if _, err = a.PurgeSearchSnapshots(); err != nil {
		return err
	}
	if _, err = a.DB.ExecContext(ctx, "UPDATE ts_recharges SET status='expired' WHERE method='trc20' AND status='pending' AND expires_at<?", now()); err != nil {
		return err
	}
	orders, err := query(a.DB, "SELECT id FROM ts_recharges WHERE method='trc20' AND status IN ('pending','expired') AND expires_at>? ORDER BY id", cDate(time.Now().Add(-24*time.Hour)))
	if err != nil {
		return err
	}
	for _, order := range orders {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, checkErr := a.CheckRecharge(ctx, integer(order["id"]), 0, "")
		if checkErr != nil {
			var ce *commerceError
			if !errors.As(checkErr, &ce) || ce.status != 409 {
				break
			}
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err = a.maintainAds(ctx); err != nil {
		return err
	}
	jobs, err := query(a.DB, "SELECT id FROM ts_ad_deliveries WHERE state='queued' ORDER BY id LIMIT 20")
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = a.deliverAd(ctx, integer(job["id"])); err != nil {
			return err
		}
	}
	if _, err = a.DB.ExecContext(ctx, "DELETE FROM ts_bot_commerce_sessions WHERE expires_at<? LIMIT 1000", cDate(time.Now().Add(-7*24*time.Hour))); err != nil {
		return err
	}
	_, err = a.DB.ExecContext(ctx, "DELETE FROM ts_bot_login_codes WHERE expires_at<?", cDate(time.Now().Add(-24*time.Hour)))
	return err
}
