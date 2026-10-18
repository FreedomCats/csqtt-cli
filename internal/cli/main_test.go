package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/config"
)

// fakeCoreEnv — с этой переменной тестовый бинарь не запускает тесты, а ведёт
// себя как ядро в заданном режиме (fakeCore). Так connect и hashes check
// проходят настоящие coreproc и supervisor, но без сервера и без VK.
const fakeCoreEnv = "CSQTT_CLI_FAKE_CORE"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeCoreEnv); mode != "" {
		os.Exit(fakeCore(mode))
	}
	// Тесты не должны дотянуться до настоящей связки ключей того, кто их
	// запускает. Хранилище в тестах подставное, но если какой-то путь его
	// минует, без шины сеанса связки нет, и секреты уйдут в файл временного
	// каталога.
	os.Unsetenv("DBUS_SESSION_BUS_ADDRESS")
	os.Unsetenv("XDG_RUNTIME_DIR")
	os.Exit(m.Run())
}

// fakeSecretSession — выдуманный токен сессии капчи, который ядро печатает в
// журнал: на экране его быть не должно.
const fakeSecretSession = "fake-session-token-0123456789"

// fakeCore — поддельное ядро. Секретов в аргументах быть не должно; первой
// строкой stdin — SECRETS:, как у настоящего (правка П1).
func fakeCore(mode string) int {
	bad := slices.ContainsFunc(os.Args, func(a string) bool {
		return strings.HasPrefix(a, "--password") || strings.HasPrefix(a, "--vk=")
	})
	if bad || !slices.Contains(os.Args, "--secrets-stdin") {
		fmt.Fprintln(os.Stderr, "[ФАТАЛ] секреты в аргументах или нет --secrets-stdin")
		return 3
	}
	in := bufio.NewScanner(os.Stdin)
	if !in.Scan() {
		return 3
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(in.Text(), "SECRETS:"))
	var secrets struct{ Password, VK string }
	if err != nil || json.Unmarshal(raw, &secrets) != nil {
		fmt.Fprintln(os.Stderr, "[ФАТАЛ] первая строка stdin — не секреты")
		return 3
	}
	hashes := strings.Split(secrets.VK, ",")
	event := func(kind, payload string) { fmt.Printf("__CSQTT_EVENT__|%s|%s\n", kind, payload) }
	waitStop := func() int {
		for in.Scan() {
			if in.Text() == "STOP" {
				break
			}
		}
		event("STOPPED", "{}")
		return 0
	}

	// rejected:<сообщение> — отказ сервера с этим сообщением ядра.
	rejectMsg := "FATAL_PROTOCOL: клиент и сервер используют разные версии протокола"
	if msg, ok := strings.CutPrefix(mode, "rejected:"); ok {
		mode, rejectMsg = "rejected", msg
	}

	switch mode {
	// hashcheck — режим --validate-vk-hashes: alive… — живой, dead… — мёртвый,
	// прочие недоступны; в сообщении недоступного — сам хеш, как в ошибке
	// сети с адресом запроса.
	case "hashcheck":
		if !slices.Contains(os.Args, "--validate-vk-hashes") || secrets.Password != "" {
			return 3
		}
		fmt.Fprintf(os.Stderr, "[VK] проверяю %s\n", hashes[0])
		for _, h := range hashes {
			switch {
			case strings.HasPrefix(h, "alive"):
				fmt.Printf("HASH_CHECK:{\"hash\":%q,\"status\":\"valid\"}\n", h)
			case strings.HasPrefix(h, "dead"):
				fmt.Printf("HASH_CHECK:{\"hash\":%q,\"status\":\"invalid\",\"code\":954,\"message\":\"Invalid join link\"}\n", h)
			default:
				fmt.Printf("HASH_CHECK:{\"hash\":%q,\"status\":\"unavailable\",\"message\":\"request for link=%s failed\"}\n", h, h)
			}
		}
		return 0

	// rejected — сервер отказал: по умолчанию другая версия протокола.
	case "rejected":
		event("PROCESS", fmt.Sprintf(`{"pid":%d}`, os.Getpid()))
		payload, _ := json.Marshal(map[string]any{"code": "handshake_rejected", "message": rejectMsg, "fatal": true})
		event("ERROR", string(payload))
		return waitStop()

	// dead — все звонки недоступны: ядро сообщает о каждом и выходит само.
	case "dead":
		event("PROCESS", fmt.Sprintf(`{"pid":%d}`, os.Getpid()))
		for _, h := range hashes {
			fmt.Fprintf(os.Stderr, "[VKCalls] звонок %s недоступен\n", h)
			event("CALL_UNAVAILABLE", fmt.Sprintf(`{"hash":%q,"code":954}`, h))
		}
		event("STOPPED", "{}")
		return 0

	// connected — туннель поднимается и работает до STOP.
	case "connected":
		event("PROCESS", fmt.Sprintf(`{"pid":%d}`, os.Getpid()))
		event("LISTEN", `{"port":9}`)
		fmt.Fprintf(os.Stderr, "[КАПЧА] solveOnce URL: https://id.vk.ru/not_robot_captcha?session_token=%s&variant=popup\n", fakeSecretSession)
		fmt.Fprintf(os.Stderr, "[КЛИЕНТ] хеш %s, пароль не печатаем\n", hashes[0])
		event("PROGRESS", `{"kind":"credentials"}`)
		event("CONFIG", `{"config":"TUNCONF:192.0.2.2:198.51.100.53"}`)
		event("READY", `{"worker":1}`)
		event("STATS", `{"active":1,"bytes_up":100,"bytes_down":200}`)
		return waitStop()

	// startup — ядро падает, не начав работу.
	case "startup":
		fmt.Fprintln(os.Stderr, "[ФАТАЛ] [КЛИЕНТ] Нужны -peer и хеши VK")
		return 1
	}
	return 2
}

// ──────────────────────────── окружение тестов ────────────────────────────

// Значения, которых в выводе быть не должно ни при каких обстоятельствах.
// Хеши выдуманы, но похожи на настоящие длиной.
const (
	testPassword = "пароль-который-нельзя-печатать"
	testHashA    = "alivehashAAAAAAAAAAAAAAAAAAAAAAA"
	testHashB    = "deadhashBBBBBBBBBBBBBBBBBBBBBBBB"
	testHashC    = "otherhashCCCCCCCCCCCCCCCCCCCCCCC"
)

// fakeStore — подставное хранилище секретов: в памяти, ссылки — fake:<имя>.
type fakeStore struct {
	mu     sync.Mutex
	values map[string]string
	// getErr — чем отвечать на Get; nil — значением.
	getErr error
}

func newFakeStore() *fakeStore { return &fakeStore{values: map[string]string{}} }

func (s *fakeStore) Put(name, value string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := "fake:" + name
	s.values[ref] = value
	return ref, nil
}

func (s *fakeStore) Get(ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return "", s.getErr
	}
	v, ok := s.values[ref]
	if !ok {
		return "", errors.New("записи нет")
	}
	return v, nil
}

func (s *fakeStore) Delete(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, ref)
	return nil
}

// syncBuffer — буфер, который читают, пока в него пишут (connect в горутине).
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// testEnv — окружение одного теста: свой каталог настроек, подставное
// хранилище, заготовленные ответы на скрытый ввод и поддельное ядро.
type testEnv struct {
	t     *testing.T
	dir   string
	store *fakeStore

	answers []string
	asked   []string
	stdin   io.Reader
	// pipe — скрытого ввода нет: секреты читаются из stdin, как в конвейере.
	pipe bool
	// core — режим поддельного ядра; пусто — ядра нет вовсе.
	core string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return &testEnv{t: t, dir: t.TempDir(), store: newFakeStore()}
}

func (te *testEnv) configPath() string { return filepath.Join(te.dir, config.FileName) }

// answer заготавливает ответы на скрытый ввод в порядке вопросов.
func (te *testEnv) answer(values ...string) *testEnv {
	te.answers = append(te.answers, values...)
	return te
}

func (te *testEnv) env(stdout, stderr io.Writer) *env {
	stdin := te.stdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	e := &env{
		stdout:      stdout,
		stderr:      stderr,
		stdin:       stdin,
		build:       Build{Version: "1.2.3", Commit: "abcdef0"},
		secretStore: func(string) config.SecretStore { return te.store },
		secrets: func(prompt string) (string, error) {
			te.asked = append(te.asked, prompt)
			if len(te.answers) == 0 {
				te.t.Errorf("лишний вопрос: %q", prompt)
				return "", errNoInput
			}
			value := te.answers[0]
			te.answers = te.answers[1:]
			return value, nil
		},
		corePath: func() (string, error) {
			if te.core == "" {
				return "", errors.New("ядра в этом тесте нет")
			}
			return os.Args[0], nil
		},
	}
	if te.pipe {
		e.secrets = nil
	}
	return e
}

// run прогоняет команду с -config своего каталога.
func (te *testEnv) run(args ...string) (code int, stdout, stderr string) {
	return te.runCtx(context.Background(), args...)
}

func (te *testEnv) runCtx(ctx context.Context, args ...string) (code int, stdout, stderr string) {
	te.t.Helper()
	if te.core != "" {
		te.t.Setenv(fakeCoreEnv, te.core)
	}
	var out, errOut syncBuffer
	code = run(ctx, te.env(&out, &errOut), append(args, "-config", te.configPath()))
	return code, out.String(), errOut.String()
}

// addHome заводит профиль home по полям, на нём стоят остальные проверки.
func (te *testEnv) addHome(extra ...string) {
	te.t.Helper()
	args := append([]string{"profile", "add", "home", "-peer", "203.0.113.10"}, extra...)
	te.answer(testPassword, testPassword, testHashA+", "+testHashB+" "+testHashC)
	if code, _, stderr := te.run(args...); code != exitOK {
		te.t.Fatalf("profile add: код %d, stderr:\n%s", code, stderr)
	}
}

// noSecrets проверяет, что в тексте нет ни пароля, ни хешей.
func noSecrets(t *testing.T, where, text string) {
	t.Helper()
	for _, secret := range []string{testPassword, testHashA, testHashB, testHashC} {
		if strings.Contains(text, secret) {
			t.Errorf("%s: в выводе секрет %q:\n%s", where, secret, text)
		}
	}
}
