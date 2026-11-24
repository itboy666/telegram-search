package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestActionBodyCompatibility(t *testing.T) {
	for _, tc := range []struct {
		input string
		valid bool
	}{
		{`{}`, true},
		{`[]`, true},
		{`{"id":9007199254740993}`, true},
		{`null`, false},
		{`[1]`, false},
		{`"hello"`, false},
		{`{} {}`, false},
		{`{} trailing`, false},
		{``, false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.input))
			value, err := Body(r)
			if (err == nil) != tc.valid {
				t.Fatalf("valid = %t, error = %v", tc.valid, err)
			}
			if tc.input == `{"id":9007199254740993}` && str(value["id"]) != "9007199254740993" {
				t.Fatal("integer precision was lost")
			}
		})
	}
}

func TestSameOriginRejectsAmbiguousHeaders(t *testing.T) {
	r := httptest.NewRequest("POST", "http://localhost:8088/user/api/password", nil)
	r.Header.Set("Origin", "http://localhost:8088")
	if !sameOrigin(r) {
		t.Fatal("same origin should be allowed")
	}
	r.Header.Add("Origin", "https://outside.example")
	if sameOrigin(r) {
		t.Fatal("duplicate origin headers must fail closed")
	}
	r.Header.Set("Origin", "http://localhost:8088, https://outside.example")
	if sameOrigin(r) {
		t.Fatal("comma-joined origin headers must fail closed")
	}
	r.Header.Set("Origin", "http://localhost:8088")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if sameOrigin(r) {
		t.Fatal("cross-site fetch must fail closed")
	}
}
