package app

import "net/http"

// Old browser tabs may still call these endpoints. Explicitly retire them
// without reading or writing historical votes or falling through to the SPA.
func (a *App) registerRetiredRecommendations() {
	retired := func(w http.ResponseWriter, _ *http.Request, _ *Identity) {
		Fail(w, http.StatusGone, "推荐功能已取消，请刷新页面。")
	}
	a.Handle("POST /user/api/resources/{id}/like", "user", "", true, retired)
	a.Handle("GET /user/api/resource-likes", "user", "", false, retired)
}
