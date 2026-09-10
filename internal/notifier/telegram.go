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
	return &Telegram{token: token, chatID: chatID, client: &http.Client{Timeout: 15 * time.Second}}
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

func (t *Telegram) Notify(ctx context.Context, message string) error {
	endpoint := "https://api.telegram.org/bot" + t.token + "/sendMessage"
	form := url.Values{"chat_id": {t.chatID}, "text": {message}, "disable_web_page_preview": {"true"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build Telegram request: invalid endpoint or context")
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
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if readErr != nil {
		return fmt.Errorf("read Telegram response failed")
	}
	var parsed telegramResponse
	parseErr := json.Unmarshal(body, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || parseErr != nil || !parsed.OK {
		// Responses and transport Status text can echo request credentials.
		// Retain only the numeric HTTP status in errors consumed by the logger.
		return fmt.Errorf("Telegram API request failed (HTTP %d)", resp.StatusCode)
	}
	return nil
}
