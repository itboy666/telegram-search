package app

import (
	"bytes"
	"context"
	"database/sql"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func proofTestPNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 12, 12))
	im.Set(1, 1, color.RGBA{R: 60, G: 100, B: 250, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRechargeProofNormalization(t *testing.T) {
	data, err := normalizeRechargeProof(proofTestPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal("sanitized image cannot decode", err)
	}
	for name, input := range map[string][]byte{
		"empty": nil, "svg": []byte(`<svg onload="alert(1)"></svg>`),
		"oversized": make([]byte, rechargeProofMaxBytes+1), "broken": {137, 80, 78, 71},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeRechargeProof(input); err == nil {
				t.Fatal("invalid attachment accepted")
			}
		})
	}
}

func TestBotCommerceScreenshotAndAccess(t *testing.T) {
	f := newBotCommerceFixture(t, 0)
	ctx := context.Background()
	id, err := catalogInsert(f.a.DB, "recharges", Row{"user_id": f.user["id"], "request_key": randomHex(16), "method": "offline", "amount": 5_000_000, "status": "pending", "created_at": now(), "expires_at": future(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.a.DB.Exec("DELETE FROM ts_recharge_proofs WHERE order_id=?", id)
		f.a.DB.Exec("DELETE FROM ts_rate_limits WHERE bucket=?", "recharge-proof:"+str(f.user["id"]))
	})
	downloads := 0
	f.a.TelegramProofDownload = func(_ context.Context, bot int64, file string) ([]byte, error) {
		downloads++
		if bot != f.botID || file != "fixture-image" {
			t.Fatal("download identity mismatch")
		}
		return proofTestPNG(t), nil
	}
	f.open("home")
	f.click("rechargeid" + str(id))
	f.click("proof")
	m := f.message(15, "")
	delete(m, "text")
	m["photo"] = []any{Row{"file_id": "fixture-image", "file_size": 120}}
	m["caption"] = "隔离测试转账截图，不含真实资金"
	if err = f.a.handleUpdate(ctx, Row{"message": m}, Row{"id": f.botID, "username": "FixtureBot"}); err != nil {
		t.Fatal(err)
	}
	f.state("recharge_detail")
	order, err := rechargeOwned(f.a.DB, id, integer(f.user["id"]), false)
	if err != nil || str(order["status"]) != "review" || downloads != 1 {
		t.Fatal("screenshot did not enter review workflow", err)
	}
	if err = f.a.annotateRechargeProof(order); err != nil || !boolean(order["has_proof_image"]) {
		t.Fatal("attachment metadata missing", err)
	}
	balance, _ := f.a.WalletBalance(integer(f.user["id"]))
	if balance != "0.000000" {
		t.Fatal("screenshot alone changed balance")
	}

	other := commerceTestUser(t, f.a)
	if _, err = f.a.rechargeTelegramProof(ctx, other, id, f.botID, m); err == nil || downloads != 1 {
		t.Fatal("foreign order downloaded a screenshot")
	}
	if _, err = f.a.rechargeTelegramProof(ctx, integer(f.user["id"]), id, f.botID+1, m); err == nil {
		t.Fatal("other bot replaced the attachment")
	}

	userToken, otherToken, adminToken := randomHex(32), randomHex(32), randomHex(32)
	adminID, err := catalogInsert(f.a.DB, "admins", Row{"username": "proof_admin_" + randomHex(6), "password_hash": "", "display_name": "Proof fixture", "status": 1, "is_super": 0, "created_at": now()})
	if err != nil {
		t.Fatal(err)
	}
	roleID, err := catalogInsert(f.a.DB, "roles", Row{"name": "proof_role_" + randomHex(6), "status": 1, "created_at": now(), "updated_at": now()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.a.DB.Exec("DELETE FROM ts_admins WHERE id=?", adminID)
		f.a.DB.Exec("DELETE FROM ts_roles WHERE id=?", roleID)
	})
	_, err = cTx(f.a, func(tx *sql.Tx) (Row, error) {
		for _, spec := range []struct {
			scope string
			id    int64
			token string
		}{{"user", integer(f.user["id"]), userToken}, {"user", other, otherToken}, {"admin", adminID, adminToken}} {
			if err := insertSession(tx, spec.scope, spec.id, spec.token, randomHex(24)); err != nil {
				return nil, err
			}
		}
		if _, err := tx.Exec("INSERT INTO ts_admin_roles(admin_id,role_id) VALUES(?,?)", adminID, roleID); err != nil {
			return nil, err
		}
		_, err := tx.Exec("INSERT INTO ts_role_permissions(role_id,permission) VALUES(?,'recharges.read')", roleID)
		return Row{}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	f.a.RegisterCommerce()
	request := func(scope, token, suffix string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", "http://localhost/"+scope+"/api/commerce/"+suffix, nil)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "starindex_" + scope, Value: token})
		}
		w := httptest.NewRecorder()
		f.a.Handler().ServeHTTP(w, req)
		return w
	}
	path := "recharges/" + str(id) + "/proof-image"
	if w := request("user", "", path); w.Code != 401 {
		t.Fatal("unauthenticated screenshot access", w.Code)
	}
	if w := request("user", otherToken, path); w.Code != 404 {
		t.Fatal("foreign screenshot access", w.Code)
	}
	if w := request("admin", adminToken, path); w.Code != 403 {
		t.Fatal("missing proof field permission bypassed", w.Code)
	}
	if w := request("admin", adminToken, "records/recharges?id="+str(id)); strings.Contains(w.Body.String(), "has_proof_image") {
		t.Fatal("attachment existence leaked without field permission")
	}
	if _, err = f.a.DB.Exec("INSERT INTO ts_role_fields(role_id,resource,field,can_read,can_write) VALUES(?,'recharges','proof',1,0)", roleID); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"user", "admin"} {
		token := userToken
		if scope == "admin" {
			token = adminToken
		}
		w := request(scope, token, path)
		if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("authorized screenshot was not served privately", w.Code)
		}
		if _, err = png.Decode(w.Body); err != nil {
			t.Fatal("invalid screenshot response", err)
		}
	}
	for _, change := range []string{"expired", "disabled"} {
		t.Run(change, func(t *testing.T) {
			if change == "expired" {
				f.a.DB.Exec("UPDATE ts_recharges SET expires_at=? WHERE id=?", future(-time.Minute), id)
			} else {
				f.a.DB.Exec("UPDATE ts_recharges SET expires_at=? WHERE id=?", future(time.Hour), id)
				f.a.DB.Exec("UPDATE ts_users SET status=0 WHERE id=?", f.user["id"])
			}
			if _, err = f.a.rechargeProof(integer(f.user["id"]), id, "新的凭证不应被接受"); err == nil {
				t.Fatal("inactive order/user accepted proof")
			}
		})
	}
}
