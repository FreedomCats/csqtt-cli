package coreproc

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeHashCheck — поддельное ядро в режиме --validate-vk-hashes: читает хеши
// из строки секретов и отвечает на каждый по его имени — alive… живой,
// dead… мёртвый (954), прочие недоступны. Хеш fatal — ядро падает, как без
// хешей.
func fakeHashCheck() int {
	if !slices.Contains(os.Args, "--validate-vk-hashes") || !slices.Contains(os.Args, "--secrets-stdin") ||
		slices.ContainsFunc(os.Args, func(a string) bool {
			return strings.HasPrefix(a, "--password") || strings.HasPrefix(a, "--vk=") || strings.HasPrefix(a, "--peer")
		}) {
		fmt.Fprintln(os.Stderr, "[ФАТАЛ] не те аргументы:", os.Args[1:])
		return 3
	}
	in := bufio.NewScanner(os.Stdin)
	if !in.Scan() {
		return 3
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(in.Text(), "SECRETS:"))
	var secrets struct{ Password, VK string }
	if err != nil || json.Unmarshal(raw, &secrets) != nil || secrets.Password != "" {
		fmt.Fprintln(os.Stderr, "[ФАТАЛ] строка секретов не та (или с паролем)")
		return 3
	}
	fmt.Fprintln(os.Stderr, "[VK] проверка хешей")
	for h := range strings.SplitSeq(secrets.VK, ",") {
		var line string
		switch {
		case h == "fatal":
			fmt.Fprintln(os.Stderr, "[ФАТАЛ] [КЛИЕНТ] Нет хешей VK для проверки")
			return 1
		case strings.HasPrefix(h, "alive"):
			line = fmt.Sprintf(`{"hash":%q,"status":"valid"}`, h)
		case strings.HasPrefix(h, "dead"):
			line = fmt.Sprintf(`{"hash":%q,"status":"invalid","code":954,"message":"Call not found"}`, h)
		default:
			line = fmt.Sprintf(`{"hash":%q,"status":"unavailable","message":"error sending request for url (https://api.vk.ru/method/x?link=https%%3A%%2F%%2Fvk.com%%2Fcall%%2Fjoin%%2F%s)"}`, h, h)
		}
		fmt.Println("HASH_CHECK:" + line)
	}
	return 0
}

func fakeCheck(ctx context.Context, t *testing.T, hashes []string) ([]HashCheck, []string, error) {
	t.Helper()
	t.Setenv(fakeCoreEnv, "hashcheck")
	var logs []string
	res, err := CheckHashes(ctx, os.Args[0], Config{Hashes: hashes, Password: "не-должен-уйти"}, func(l Log) {
		logs = append(logs, l.Text)
	})
	return res, logs, err
}

func TestCheckHashes(t *testing.T) {
	res, logs, err := fakeCheck(context.Background(), t, []string{"alive-1", "dead-2", "other-3"})
	if err != nil {
		t.Fatalf("CheckHashes: %v (журнал %q)", err, logs)
	}
	want := []HashCheck{
		{Hash: "alive-1", Status: HashValid},
		{Hash: "dead-2", Status: HashInvalid, Code: 954, Message: "Call not found"},
		{Hash: "other-3", Status: HashUnavailable},
	}
	if len(res) == 3 && strings.HasPrefix(res[2].Message, "error sending request") {
		res[2].Message = "" // текст ошибки сети — не предмет проверки
	}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("итоги:\n%+v\nожидалось:\n%+v", res, want)
	}
	if !slices.Contains(logs, "[VK] проверка хешей") {
		t.Errorf("журнал ядра не дошёл: %q", logs)
	}
}

// Ядро упало — ошибка с концом его журнала: в нём причина.
func TestCheckHashesCoreFails(t *testing.T) {
	res, _, err := fakeCheck(context.Background(), t, []string{"alive-1", "fatal"})
	if err == nil || !strings.Contains(err.Error(), "Нет хешей VK") {
		t.Fatalf("ошибка = %v", err)
	}
	if len(res) != 1 {
		t.Errorf("итог до падения потерян: %+v", res)
	}
}

// Отмена — ядро завершается сразу: команд stdin в этом режиме оно не читает.
func TestCheckHashesCancel(t *testing.T) {
	t.Setenv(fakeCoreEnv, "deaf")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err := CheckHashes(ctx, os.Args[0], Config{Hashes: []string{"h"}}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка = %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("отмена заняла %v", d)
	}
}

func TestHashCheckArgs(t *testing.T) {
	cfg := Config{Peer: "203.0.113.10:46000", Password: "p", Hashes: []string{"h"}, Workers: 18,
		Fingerprint: "firefox", ClientIDs: []string{"1", "2"}}
	want := []string{"--secrets-stdin", "--validate-vk-hashes", "--fingerprint=firefox", "--client-ids=1,2"}
	if got := cfg.HashCheckArgs(); !reflect.DeepEqual(got, want) {
		t.Errorf("HashCheckArgs = %q", got)
	}
}
