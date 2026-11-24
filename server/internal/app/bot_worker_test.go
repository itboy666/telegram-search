package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestBotCallbackBusinessErrorsAreAnswered(t *testing.T) {
	for _, failure := range []error{
		catalogErr(403, "当前账号已停用。"),
		fmt.Errorf("callback: %w", catalogErr(422, strings.Repeat("查询无效", 80))),
		cErr(409, "订单状态已经变化。"),
	} {
		a := &App{}
		calls := []Row{}
		a.TelegramCall = func(_ context.Context, method string, p Row) (Row, error) {
			if method != "answerCallbackQuery" {
				t.Fatalf("unexpected method %s", method)
			}
			calls = append(calls, p)
			if len(calls) == 1 {
				return nil, failure
			}
			return Row{"ok": true}, nil
		}
		// An invalid callback needs no database; the injected domain error exercises
		// the same dispatch boundary as a business refusal from search or commerce.
		err := a.handleUpdate(context.Background(), Row{"callback_query": Row{"id": "fixture", "data": "unknown"}}, Row{"id": 1})
		if err != nil || len(calls) != 2 {
			t.Fatalf("business refusal must be answered once and complete: calls=%d err=%v", len(calls), err)
		}
		last := calls[1]
		if str(last["callback_query_id"]) != "fixture" || !boolean(last["show_alert"]) || str(last["text"]) != clip(failure.Error(), 200) {
			t.Fatal("business alert was not delivered to the original callback")
		}
	}
}

func TestBotCallbackTransportFailureRemainsRetryable(t *testing.T) {
	want := errors.New("network unavailable")
	a := &App{}
	calls := 0
	a.TelegramCall = func(_ context.Context, _ string, _ Row) (Row, error) {
		calls++
		return nil, want
	}
	err := a.handleUpdate(context.Background(), Row{"callback_query": Row{"id": "fixture", "data": "unknown"}}, Row{"id": 1})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatal("transport failure was swallowed or exposed as a business alert")
	}
}

func TestBotCallbackExpiredBusinessAlertCompletes(t *testing.T) {
	a := &App{}
	calls := 0
	a.TelegramCall = func(_ context.Context, _ string, _ Row) (Row, error) {
		calls++
		if calls == 1 {
			return nil, catalogErr(403, "当前账号已停用。")
		}
		return nil, &TelegramError{Code: 400, Description: "query is too old"}
	}
	if err := a.handleUpdate(context.Background(), Row{"callback_query": Row{"id": "fixture", "data": "unknown"}}, Row{"id": 1}); err != nil || calls != 2 {
		t.Fatal("expired callback must not keep the worker retrying", err)
	}
}
