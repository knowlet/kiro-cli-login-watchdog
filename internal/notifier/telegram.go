package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Telegram struct {
	token  string
	chatID string
	client *http.Client
}

func NewTelegram(token, chatID string) *Telegram {
	return &Telegram{
		token:  token,
		chatID: chatID,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

func (t *Telegram) Notify(ctx context.Context, message string) error {
	endpoint := "https://api.telegram.org/bot" + t.token + "/sendMessage"
	form := url.Values{
		"chat_id":                  {t.chatID},
		"text":                     {message},
		"disable_web_page_preview": {"true"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build Telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.client.Do(req)
	if err != nil {
		// net/http errors may include the full request URL. The Telegram bot
		// token is part of that URL, so never wrap the raw error here.
		if ctx.Err() != nil {
			return fmt.Errorf("send Telegram message: %w", ctx.Err())
		}
		return fmt.Errorf("send Telegram message: network request failed")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var parsed telegramResponse
	_ = json.Unmarshal(body, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !parsed.OK {
		description := parsed.Description
		if description == "" {
			description = strings.TrimSpace(string(body))
		}
		return fmt.Errorf("Telegram API returned %s: %s", resp.Status, description)
	}
	return nil
}
