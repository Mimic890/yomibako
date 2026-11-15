// Package telegram is a minimal Telegram Bot API client: long polling, sending and editing
// messages. It exists so the service has no dependency beyond the standard library.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client talks to the Bot API.
type Client struct {
	Token string
	HTTP  *http.Client
	Base  string
}

// New returns a client for a bot token.
func New(token string) *Client {
	return &Client{
		Token: token,
		HTTP:  &http.Client{Timeout: 60 * time.Second},
		Base:  "https://api.telegram.org",
	}
}

// Update is one incoming update.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

// Message is an incoming message.
type Message struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text"`
	Chat      Chat   `json:"chat"`
	From      *User  `json:"from"`
}

// Chat is a chat the bot talks to.
type Chat struct {
	ID int64 `json:"id"`
}

// User is the sender.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func (c *Client) call(ctx context.Context, method string, params url.Values, out any) error {
	endpoint := fmt.Sprintf("%s/bot%s/%s", c.Base, c.Token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	var parsed apiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("telegram: %s: invalid response: %w", method, err)
	}
	if !parsed.OK {
		return fmt.Errorf("telegram: %s: %s", method, parsed.Description)
	}
	if out != nil && len(parsed.Result) > 0 {
		return json.Unmarshal(parsed.Result, out)
	}
	return nil
}

// Me returns the bot's own account, which is also a cheap way to validate the token.
func (c *Client) Me(ctx context.Context) (User, error) {
	var user User
	err := c.call(ctx, "getMe", url.Values{}, &user)
	return user, err
}

// GetUpdates long-polls for new updates.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	params := url.Values{}
	params.Set("offset", strconv.FormatInt(offset, 10))
	params.Set("timeout", strconv.Itoa(int(timeout.Seconds())))
	params.Set("allowed_updates", `["message"]`)

	var updates []Update
	if err := c.call(ctx, "getUpdates", params, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// SendMessage sends a message and returns its id.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) (int64, error) {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(chatID, 10))
	params.Set("text", text)
	params.Set("parse_mode", "HTML")
	params.Set("disable_web_page_preview", "true")

	var message Message
	if err := c.call(ctx, "sendMessage", params, &message); err != nil {
		return 0, err
	}
	return message.MessageID, nil
}

// EditMessage replaces the text of a message. Telegram rejects edits that change nothing, so that
// error is reported as nil.
func (c *Client) EditMessage(ctx context.Context, chatID, messageID int64, text string) error {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(chatID, 10))
	params.Set("message_id", strconv.FormatInt(messageID, 10))
	params.Set("text", text)
	params.Set("parse_mode", "HTML")
	params.Set("disable_web_page_preview", "true")

	err := c.call(ctx, "editMessageText", params, nil)
	if err != nil && bytes.Contains([]byte(err.Error()), []byte("message is not modified")) {
		return nil
	}
	return err
}

// SetMyCommands publishes the command list shown in the client.
func (c *Client) SetMyCommands(ctx context.Context, commands [][2]string) error {
	type command struct {
		Command     string `json:"command"`
		Description string `json:"description"`
	}
	list := make([]command, 0, len(commands))
	for _, item := range commands {
		list = append(list, command{Command: item[0], Description: item[1]})
	}
	payload, err := json.Marshal(list)
	if err != nil {
		return err
	}
	params := url.Values{}
	params.Set("commands", string(payload))
	return c.call(ctx, "setMyCommands", params, nil)
}
