// Package telegram is a minimal Telegram Bot API client: long polling, sending and editing
// messages. It exists so the service has no dependency beyond the standard library.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
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
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// CallbackQuery is a press on an inline button.
type CallbackQuery struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Message *Message `json:"message"`
	From    *User    `json:"from"`
}

// Button is one inline keyboard button.
type Button struct {
	Text string
	Data string
}

// Message is an incoming message.
type Message struct {
	MessageID int64     `json:"message_id"`
	Text      string    `json:"text"`
	Chat      Chat      `json:"chat"`
	From      *User     `json:"from"`
	Document  *Document `json:"document"`
}

// Document is a file sent to the bot.
type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
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

// Download fetches a file the user sent, in full. The Bot API caps this at 20 MB, which a cookies
// export never approaches.
func (c *Client) Download(ctx context.Context, fileID string) ([]byte, error) {
	var file struct {
		FilePath string `json:"file_path"`
	}
	if err := c.call(ctx, "getFile", url.Values{"file_id": {fileID}}, &file); err != nil {
		return nil, err
	}
	if file.FilePath == "" {
		return nil, fmt.Errorf("telegram: файл недоступен")
	}

	endpoint := fmt.Sprintf("%s/file/bot%s/%s", c.Base, c.Token, file.FilePath)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram: файл не скачался: HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 4<<20))
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
	params.Set("allowed_updates", `["message","callback_query"]`)

	var updates []Update
	if err := c.call(ctx, "getUpdates", params, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// SendMessage sends a message and returns its id.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) (int64, error) {
	return c.SendMessageWithKeyboard(ctx, chatID, text, nil)
}

// SendMessageWithKeyboard sends a message with inline buttons.
func (c *Client) SendMessageWithKeyboard(ctx context.Context, chatID int64, text string, buttons [][]Button) (int64, error) {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(chatID, 10))
	params.Set("text", text)
	params.Set("parse_mode", "HTML")
	params.Set("disable_web_page_preview", "true")
	// Telegram drops the whole message when one button carries more than 64 bytes, and its own
	// error says nothing about which one, so the limit is checked here.
	for _, row := range buttons {
		for _, button := range row {
			if len(button.Data) > 64 {
				return 0, fmt.Errorf("telegram: callback data of %q is %d bytes, the limit is 64",
					button.Text, len(button.Data))
			}
		}
	}
	if len(buttons) > 0 {
		keyboard := make([][]map[string]string, 0, len(buttons))
		for _, row := range buttons {
			line := make([]map[string]string, 0, len(row))
			for _, button := range row {
				line = append(line, map[string]string{"text": button.Text, "callback_data": button.Data})
			}
			keyboard = append(keyboard, line)
		}
		payload, err := json.Marshal(map[string]any{"inline_keyboard": keyboard})
		if err != nil {
			return 0, err
		}
		params.Set("reply_markup", string(payload))
	}

	var message Message
	if err := c.call(ctx, "sendMessage", params, &message); err != nil {
		return 0, err
	}
	return message.MessageID, nil
}

// SendRichMessage sends a rich message built from blocks and returns its id.
func (c *Client) SendRichMessage(ctx context.Context, chatID int64, blocks []map[string]any) (int64, error) {
	payload, err := json.Marshal(map[string]any{"blocks": blocks})
	if err != nil {
		return 0, err
	}
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(chatID, 10))
	params.Set("rich_message", string(payload))

	var message Message
	if err := c.call(ctx, "sendRichMessage", params, &message); err != nil {
		return 0, err
	}
	return message.MessageID, nil
}

// AnswerCallback stops the button spinner in the client.
func (c *Client) AnswerCallback(ctx context.Context, id, text string) error {
	params := url.Values{}
	params.Set("callback_query_id", id)
	if text != "" {
		params.Set("text", text)
	}
	return c.call(ctx, "answerCallbackQuery", params, nil)
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

// SendDocument uploads a file to a chat. The Bot API wants multipart here, not the urlencoded form
// the other methods use.
func (c *Client) SendDocument(ctx context.Context, chatID int64, name string, body []byte, caption string) error {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)

	_ = writer.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if caption != "" {
		_ = writer.WriteField("caption", caption)
		_ = writer.WriteField("parse_mode", "HTML")
	}
	part, err := writer.CreateFormFile("document", name)
	if err != nil {
		return err
	}
	if _, err := part.Write(body); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendDocument", c.Base, c.Token)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buffer)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())

	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	var payload apiResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return err
	}
	if !payload.OK {
		return fmt.Errorf("telegram: %s", payload.Description)
	}
	return nil
}
