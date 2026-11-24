package app

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMoneyExactness(t *testing.T) {
	cases := map[string]int64{"1": 1_000_000, "0.000001": 1, "1000000.000000": moneyMax, "001": -1, "1e3": -1, "1.0000001": -1, "-1": -1, "NaN": -1, "1000001": -1}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			got, err := ParseMoney(input, false)
			if want < 0 {
				if err == nil {
					t.Fatal("accepted invalid amount")
				}
				return
			}
			if err != nil || got != want {
				t.Fatalf("got %d, %v; want %d", got, err, want)
			}
			again, err := ParseMoney(FormatMoney(got), false)
			if err != nil || again != got {
				t.Fatal("decimal roundtrip lost precision")
			}
		})
	}
	if _, err := ParseMoney(float64(0.1), false); err == nil {
		t.Fatal("float accepted")
	}
	if n, err := ParseMoney(json.Number("0.1"), false); err != nil || n != 100000 {
		t.Fatal("JSON number precision lost")
	}
	if _, err := ParseMoney("0", false); err == nil {
		t.Fatal("nonpositive payment accepted")
	}
	if n, err := ParseMoney("0", true); err != nil || n != 0 {
		t.Fatal("allowed zero rejected")
	}
	if FormatMoney(math.MinInt64) != "-9223372036854.775808" {
		t.Fatal("MinInt64 overflow")
	}
	if FormatMoney(-1) != "-0.000001" {
		t.Fatal("negative ledger formatting")
	}
	if _, err := safeBalance(0, -1); err == nil {
		t.Fatal("overdraft allowed")
	}
	if _, err := safeBalance(math.MaxInt64, 1); err == nil {
		t.Fatal("overflow allowed")
	}
	if _, err := safeBalance(0, moneyMax+1); err == nil {
		t.Fatal("max amount bypass")
	}
}

func TestTRONAddressChecksum(t *testing.T) {
	if !ValidTRONAddress(usdtTRC20) {
		t.Fatal("valid mainnet address rejected")
	}
	if ValidTRONAddress(usdtTRC20[:33] + "u") {
		t.Fatal("bad checksum accepted")
	}
	if ValidTRONAddress("0x0123456789") {
		t.Fatal("non-TRON address accepted")
	}
}

func commerceProofFixture() (Row, Row, string) {
	txid := strings.Repeat("a", 64)
	created := cDate(time.Now().Add(-10 * time.Minute))
	expires := cDate(time.Now().Add(20 * time.Minute))
	order := Row{"receive_address": usdtTRC20, "amount": int64(50_123_456), "created_at": created, "expires_at": expires}
	proof := Row{"hash": txid, "contractRet": "SUCCESS", "confirmed": true, "revert": false, "confirmations": int64(19), "timestamp": time.Now().UnixMilli(),
		"trc20TransferInfo": []any{Row{"contract_address": usdtTRC20, "to_address": usdtTRC20, "type": "Transfer", "tokenType": "trc20", "decimals": int64(6), "amount_str": "50123456"}}}
	return proof, order, txid
}

func TestTRC20ProofRejectsMismatches(t *testing.T) {
	proof, order, txid := commerceProofFixture()
	if err := verifyTRC20(proof, order, txid); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(Row, Row)
	}{
		{"wrong_hash", func(p, o Row) { p["hash"] = strings.Repeat("b", 64) }},
		{"failed", func(p, o Row) { p["contractRet"] = "REVERT" }},
		{"unconfirmed", func(p, o Row) { p["confirmed"] = false }},
		{"confirmed_string", func(p, o Row) { p["confirmed"] = "true" }},
		{"missing_revert", func(p, o Row) { delete(p, "revert") }},
		{"reverted", func(p, o Row) { p["revert"] = true }},
		{"too_few_confirmations", func(p, o Row) { p["confirmations"] = 18 }},
		{"before_order", func(p, o Row) { p["timestamp"] = cTime(o["created_at"]).UnixMilli() - 1 }},
		{"after_expiry", func(p, o Row) { p["timestamp"] = cTime(o["expires_at"]).UnixMilli() + 1 }},
		{"invalid_order_time", func(p, o Row) { o["created_at"] = "not a date" }},
		{"wrong_address", func(p, o Row) { o["receive_address"] = "another" }},
		{"wrong_amount", func(p, o Row) { o["amount"] = int64(50_000_000) }},
		{"wrong_contract", func(p, o Row) { p["trc20TransferInfo"].([]any)[0].(Row)["contract_address"] = "fake" }},
		{"wrong_decimals", func(p, o Row) { p["trc20TransferInfo"].([]any)[0].(Row)["decimals"] = 18 }},
		{"missing_raw_amount", func(p, o Row) { delete(p["trc20TransferInfo"].([]any)[0].(Row), "amount_str") }},
		{"ambiguous_transfer", func(p, o Row) {
			items := p["trc20TransferInfo"].([]any)
			p["trc20TransferInfo"] = append(items, items[0])
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			p, o, h := commerceProofFixture()
			test.mutate(p, o)
			if err := verifyTRC20(p, o, h); err == nil {
				t.Fatal("invalid proof accepted")
			}
		})
	}
}

func TestCommerceSecretEnvelope(t *testing.T) {
	dir := t.TempDir()
	plain := "local-test-key-not-a-real-provider-key"
	envelope, err := encryptSecret(dir, ".commerce.key", plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(envelope, plain) {
		t.Fatal("plaintext leaked")
	}
	decoded, err := decryptSecret(dir, ".commerce.key", envelope)
	if err != nil || decoded != plain {
		t.Fatal("secret roundtrip failed")
	}
	bad := envelope[:len(envelope)-5] + "AAAAA"
	if _, err = decryptSecret(dir, ".commerce.key", bad); err == nil {
		t.Fatal("tampered envelope accepted")
	}
	if _, err = decryptSecret(filepath.Join(dir, "missing"), ".commerce.key", envelope); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestAdValidation(t *testing.T) {
	if _, _, _, _, err := validateAd(Row{"title": "安全广告", "url": "https://example.com", "keyword": " Go "}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []Row{
		{"title": "bad\nline", "url": "https://example.com"},
		{"title": "name", "url": "http://example.com"},
		{"title": "name", "url": "https://user:password@example.com"},
		{"title": "name", "url": "javascript:alert(1)"},
		{"title": strings.Repeat("长", 61), "url": "https://example.com"},
	} {
		if _, _, _, _, err := validateAd(input); err == nil {
			t.Fatal("invalid ad accepted")
		}
	}
	html := adHTML(Row{"title": "<b>", "url": "https://example.com/?a=\"x\""})
	if strings.Contains(html, "<b>") || strings.Contains(html, `a="x"`) {
		t.Fatal("HTML not escaped")
	}
}
