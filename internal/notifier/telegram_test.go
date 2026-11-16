package notifier

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTelegramErrorsDoNotExposeCredentials(t *testing.T) {
	const secret = "private-bot-token"
	const code = "ABCD-EFGH"
	for _, mode := range []string{"transport", "json", "body", "status", "malformed", "endpoint"} {
		t.Run(mode, func(t *testing.T) {
			token := secret
			if mode == "endpoint" {
				token += "%invalid"
			}
			n := NewTelegram(token, "test-chat")
			n.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if mode == "transport" {
					return nil, errors.New(req.URL.String() + code)
				}
				status, body := "400 "+secret+code, secret+code
				if mode == "json" {
					body = `{"ok":false,"description":"` + secret + code + `"}`
				}
				if mode == "malformed" {
					body = "not-json"
				}
				return &http.Response{StatusCode: 400, Status: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			err := n.Notify(context.Background(), code)
			if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), code) {
				t.Fatalf("unsafe or absent error: %v", err)
			}
		})
	}
}

func TestTelegramSuccessAndCancellation(t *testing.T) {
	n := NewTelegram("fake-token", "fake-chat")
	n.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := req.ParseForm(); err != nil {
			return nil, err
		}
		if req.Method != http.MethodPost || req.Form.Get("chat_id") != "fake-chat" || req.Form.Get("text") != "hello" {
			t.Error("incorrect notification request")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})
	if err := n.Notify(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	n.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) { return nil, req.Context().Err() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := n.Notify(ctx, "hello"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestTelegramDoesNotFollowRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			n := NewTelegram("synthetic-bot-token", "synthetic-chat")
			requests := 0
			n.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				if requests > 1 {
					t.Errorf("followed redirect to %s with method %s", req.URL.Host, req.Method)
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Location": {"https://untrusted.invalid/collect"}}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
			})
			err := n.Notify(context.Background(), "Synthetic device code: ABCD-EFGH")
			if err == nil {
				t.Fatal("redirect must not count as a delivered notification")
			}
			if requests != 1 {
				t.Fatalf("sent %d requests, want one", requests)
			}
		})
	}
}
