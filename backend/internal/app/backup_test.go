package app_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"tracking/internal/app"
)

// The CLI crosses real pg_dump/S3/pg_restore boundaries; API and Telegram
// observations verify the recovered application, rather than comparing SQL rows.
func TestBackupRestoresApplicationAndWorkers(t *testing.T) {
	if os.Getenv("TEST_BACKUP_RESTORE") != "1" {
		t.Skip("requires compose.backup-test.yaml; run scripts/check.sh")
	}
	a, bot, f := digestScenario(t)
	connectDigest(t, a, bot, f)
	sub := createDigestSubscription(a, "Сохранённая подписка", "2028-02-29")
	archived := createDigestSubscription(a, "Архивная подписка", "2028-03-01")
	a.request("POST", "/api/subscriptions/"+archived.ID+"/archive", "", 200)
	runDigest(t, a, bot, "2028-02-29T07:00:00Z")
	createDigestSubscription(a, "Сохранённый повтор", "2028-03-01")
	f.failure = true
	at := time.Date(2028, 3, 1, 7, 0, 0, 0, time.UTC)
	if err := app.NewDigestWorker(testDB(t), a.config, bot).Cycle(context.Background(), at); err == nil {
		t.Fatal("expected uncertain delivery")
	}
	// Each invocation gets a clean database in the independent recovery server.
	targetName := fmt.Sprintf("restore_%d", time.Now().UnixNano())
	admin, err := sql.Open("postgres", "postgres://tracking:test-only@127.0.0.1:55433/restored?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP DATABASE ` + targetName + ` WITH (FORCE)`); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	if _, err := admin.Exec(`CREATE DATABASE ` + targetName); err != nil {
		t.Fatal(err)
	}
	cli := func(success bool, args ...string) map[string]any {
		t.Helper()
		base := []string{"compose", "-f", "../../../compose.test.yaml", "-f", "../../../compose.backup-test.yaml", "run", "--rm", "--no-deps", "-e", "RESTORE_DATABASE_URL=postgres://tracking:test-only@restore-db:5432/" + targetName + "?sslmode=disable", "backup-test"}
		cmd := exec.Command("docker", append(base, args...)...)
		output, err := cmd.Output()
		if (err == nil) != success {
			t.Fatalf("backup CLI %v: %v %s", args, err, output)
		}
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		var result map[string]any
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &result); err != nil {
			t.Fatalf("invalid CLI result: %v %s", err, output)
		}
		return result
	}
	backup := cli(true, "backup")["backup"].(string)
	// A change after the snapshot must not appear in the restored application.
	a.request("PATCH", "/api/subscriptions/"+sub.ID, `{"name":"После копирования","amount":"2.00"}`, 200)
	cli(true, "restore", backup)
	if cli(false, "restore", backup)["stage"] != "target_not_empty" {
		t.Fatal("restore must refuse an occupied target")
	}
	dsn, _ := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	dsn.Host = "127.0.0.1:55433"
	dsn.Path = "/" + targetName
	t.Setenv("TEST_DATABASE_URL", dsn.String())
	*a.handler = *app.New(testDB(t), a.config, &browserProvider{})
	a.handler.Now = func() time.Time { return a.now }
	if restored := a.find(sub.ID, "active"); restored == nil || restored.Name != "Сохранённая подписка" || restored.Amount != "12345678901234567890.01" {
		t.Fatal("subscription snapshot lost")
	}
	if a.find(archived.ID, "archived") == nil {
		t.Fatal("archive lost")
	}
	if body := string(a.request("GET", "/api/telegram/connection", "", 200)); !strings.Contains(body, `"status":"connected"`) {
		t.Fatal("connection lost")
	}
	f.failure = false
	f.messages = nil
	f.recipients = nil
	var offset int64
	bot.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			var request struct{ Offset int64 }
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			offset = request.Offset
		}
		return f.transport(r)
	})}
	if err := app.NewBotWorker(testDB(t), a.config, bot).Poll(context.Background()); err != nil || offset != 2 {
		t.Fatalf("update position lost: offset=%d err=%v", offset, err)
	}
	runDigest(t, a, bot, "2028-02-29T14:00:00Z")
	if len(f.messages) != 0 {
		t.Fatal("restored successful digest was resent")
	}
	runDigest(t, a, bot, "2028-03-01T07:00:59Z")
	if len(f.messages) != 0 {
		t.Fatal("restored retry ignored durable delay")
	}
	runDigest(t, a, bot, "2028-03-01T07:01:01Z")
	runDigest(t, a, bot, "2028-03-01T14:00:00Z")
	if len(f.messages) != 1 || !strings.Contains(f.messages[0], "Сохранённый повтор") || f.recipients[0] != 777006 {
		t.Fatal("restored journal/recipient did not resume once")
	}
}
