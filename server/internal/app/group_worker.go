package app

import (
	"context"
	"log"
	"time"
)

// RunGroupManagement owns one bounded sweep loop for the Bot worker. Challenges
// live in MySQL; a restart does not lose deadlines or allocate one timer per user.
func (a *App) RunGroupManagement(ctx context.Context) {
	defer a.setWorkerState("group_management", Row{"state": "stopped", "heartbeat": time.Now().Unix()})
	for ctx.Err() == nil {
		state := Row{"state": "running", "heartbeat": time.Now().Unix(), "last_error": nil}
		if err := a.GroupManagementOnce(ctx); err != nil && ctx.Err() == nil {
			log.Print("group management: 本轮任务未完成，保留待处理记录等待重试")
			state["last_error"] = "群管任务暂未完成，请检查群权限与网络。"
			state["state"] = "retrying"
		}
		a.setWorkerState("group_management", state)
		if !waitContext(ctx, 5*time.Second) {
			return
		}
	}
}
