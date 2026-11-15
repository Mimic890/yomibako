package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mimic890/yomibako/internal/config"
	"github.com/Mimic890/yomibako/internal/store"
)

// fakeTelegram answers the Bot API methods the bot uses and records outgoing messages.
type fakeTelegram struct {
	mu       sync.Mutex
	sent     []string
	updates  []string
	updateID int64
}

func (f *fakeTelegram) handler(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	switch method {
	case "getUpdates":
		f.mu.Lock()
		var result []map[string]any
		if len(f.updates) > 0 {
			text := f.updates[0]
			f.updates = f.updates[1:]
			f.updateID++
			result = append(result, map[string]any{
				"update_id": f.updateID,
				"message": map[string]any{
					"message_id": 1,
					"text":       text,
					"chat":       map[string]any{"id": 42},
				},
			})
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	case "getMe":
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"id": 1, "username": "testbot"}})
	case "sendRichMessage":
		f.mu.Lock()
		f.sent = append(f.sent, "rich")
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 8}})
	case "sendMessage":
		_ = r.ParseForm()
		f.mu.Lock()
		f.sent = append(f.sent, r.Form.Get("text"))
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 7}})
	default:
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true})
	}
}

func (f *fakeTelegram) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func TestBotAnswersList(t *testing.T) {
	fake := &fakeTelegram{updates: []string{"/list"}}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer server.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.Telegram = config.Telegram{Token: "test", ChatID: 42}

	b := New(cfg, nil, st)
	b.api.Base = server.URL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.messages()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	var answer string
	for _, message := range fake.messages() {
		if strings.Contains(message, "пусто") {
			answer = message
		}
	}
	if answer == "" {
		t.Fatalf("bot did not answer the command: %v", fake.messages())
	}
}

func TestBotIgnoresForeignChat(t *testing.T) {
	fake := &fakeTelegram{updates: []string{"/list"}}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer server.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.Telegram = config.Telegram{Token: "test", ChatID: 999}

	b := New(cfg, nil, st)
	b.api.Base = server.URL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	time.Sleep(300 * time.Millisecond)
	for _, message := range fake.messages() {
		if message != "бот перезапущен" {
			t.Fatalf("bot answered a foreign chat: %q", message)
		}
	}
}

func TestBotRequiresConfiguration(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	b := New(config.Default(), nil, st)
	if err := b.Run(context.Background()); err == nil {
		t.Fatal("expected an error without a token")
	}
}

func TestBotCancelWithoutJob(t *testing.T) {
	fake := &fakeTelegram{updates: []string{"/cancel"}}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer server.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.Telegram = config.Telegram{Token: "test", ChatID: 42}

	b := New(cfg, nil, st)
	b.api.Base = server.URL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(fake.messages()) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	var answer string
	for _, message := range fake.messages() {
		if strings.Contains(message, "нечего отменять") {
			answer = message
		}
	}
	if answer == "" {
		t.Fatalf("unexpected answer: %v", fake.messages())
	}
}

func TestBotStoresToken(t *testing.T) {
	fake := &fakeTelegram{updates: []string{"/token abcdefghijklmnopqrstuvwxyz"}}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer server.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.Telegram = config.Telegram{Token: "test", ChatID: 42}

	b := New(cfg, nil, st)
	b.api.Base = server.URL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(fake.messages()) == 0 {
		time.Sleep(20 * time.Millisecond)
	}

	saved, ok, err := st.Setting(context.Background(), "ranobelib_token")
	if err != nil || !ok || saved != "abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("token was not stored: %q, %v, %v", saved, ok, err)
	}
}
