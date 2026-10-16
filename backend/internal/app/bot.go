package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// BotAPI is the external Telegram boundary; production always uses api.telegram.org.
type BotAPI interface {
	Updates(context.Context, int64) ([]BotUpdate, error)
	Send(context.Context, int64, string) error
}
type BotUpdate struct {
	ID      int64 `json:"update_id"`
	Message *struct {
		From *struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}
type BotError struct {
	Permanent  bool
	RetryAfter time.Duration
}

func (*BotError) Error() string { return "Telegram bot request failed" }

type Bot struct {
	token  string
	Client *http.Client
}

func NewBot(c Config) *Bot {
	return &Bot{token: c.BotToken, Client: &http.Client{Timeout: 35 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (b *Bot) request(ctx context.Context, method string, body any, dest any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return &BotError{}
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.telegram.org/bot"+b.token+"/"+method, strings.NewReader(string(data)))
	if err != nil {
		return &BotError{}
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := b.Client.Do(req)
	if err != nil {
		return &BotError{}
	}
	defer res.Body.Close()
	var envelope struct {
		OK         bool            `json:"ok"`
		Code       int             `json:"error_code"`
		Result     json.RawMessage `json:"result"`
		Parameters struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&envelope) != nil {
		return &BotError{}
	}
	if !envelope.OK || res.StatusCode != 200 {
		code := envelope.Code
		if code == 0 {
			code = res.StatusCode
		}
		retry := envelope.Parameters.RetryAfter
		if retry < 0 {
			retry = 0
		}
		if retry > 86400 {
			retry = 86400
		}
		return &BotError{Permanent: code == 400 || code == 401 || code == 403, RetryAfter: time.Duration(retry) * time.Second}
	}
	if dest != nil && json.Unmarshal(envelope.Result, dest) != nil {
		return &BotError{}
	}
	return nil
}
func (b *Bot) Updates(ctx context.Context, offset int64) ([]BotUpdate, error) {
	var updates []BotUpdate
	err := b.request(ctx, "getUpdates", map[string]any{"offset": offset, "limit": 1, "timeout": 25, "allowed_updates": []string{"message"}}, &updates)
	return updates, err
}
func (b *Bot) Send(ctx context.Context, chat int64, text string) error {
	return b.request(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": text}, nil)
}

type BotWorker struct {
	db       *sql.DB
	config   Config
	telegram BotAPI
}

func NewBotWorker(db *sql.DB, c Config, telegram BotAPI) *BotWorker {
	return &BotWorker{db: db, config: c, telegram: telegram}
}

// Poll handles at most one update. The row lock serializes pollers through
// receipt, external send and checkpoint. A crash during send may cause the
// agreed rare duplicate; confirmed, committed sends are never replayed.
func (b *BotWorker) Poll(ctx context.Context) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO telegram_connections(owner_id) VALUES($1) ON CONFLICT DO NOTHING`, b.config.OwnerID)
	if err != nil {
		return err
	}
	var offset int64
	if err = tx.QueryRowContext(ctx, `SELECT next_update_id FROM telegram_connections WHERE owner_id=$1 FOR UPDATE`, b.config.OwnerID).Scan(&offset); err != nil {
		return err
	}
	updates, err := b.telegram.Updates(ctx, offset)
	if err != nil {
		var failure *BotError
		if errors.As(err, &failure) && failure.Permanent {
			if _, saveErr := tx.ExecContext(ctx, `UPDATE telegram_connections SET status='error' WHERE owner_id=$1`, b.config.OwnerID); saveErr != nil {
				return saveErr
			}
			if saveErr := tx.Commit(); saveErr != nil {
				return saveErr
			}
		}
		return err
	}
	for _, u := range updates {
		if u.ID < offset {
			continue
		}
		var sendErr error
		m := u.Message
		if m != nil && m.From != nil && m.From.ID == b.config.OwnerID && m.Chat.Type == "private" && m.Chat.ID > 0 && b.startCommand(m.Text) {
			sendErr = b.telegram.Send(ctx, m.Chat.ID, "Проверка связи: бот «Рядом» подключён. Здесь будут напоминания о плановых платежах.")
			status := "connected"
			if sendErr != nil {
				var failure *BotError
				if errors.As(sendErr, &failure) && failure.Permanent {
					status = "error"
				} else {
					status = "checking"
				}
			}
			if _, err = tx.ExecContext(ctx, `UPDATE telegram_connections SET chat_id=$2,status=$3,connection_revision=connection_revision+1 WHERE owner_id=$1`, b.config.OwnerID, m.Chat.ID, status); err != nil {
				return err
			}
			if status == "checking" {
				if err = tx.Commit(); err != nil {
					return err
				}
				return sendErr
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE telegram_connections SET next_update_id=$2 WHERE owner_id=$1`, b.config.OwnerID, u.ID+1); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (b *BotWorker) startCommand(text string) bool {
	words := strings.Fields(text)
	if len(words) == 0 {
		return false
	}
	return words[0] == "/start" || strings.EqualFold(words[0], "/start@"+b.config.BotUsername)
}
func (s *Server) botConnection(w http.ResponseWriter, r *http.Request) {
	status := "disconnected"
	err := s.db.QueryRowContext(r.Context(), `SELECT status FROM telegram_connections WHERE owner_id=$1`, s.config.OwnerID).Scan(&status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, 503, "unavailable", "Не удалось проверить подключение Telegram")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]string{"status": status, "botUrl": "https://t.me/" + s.config.BotUsername})
}
