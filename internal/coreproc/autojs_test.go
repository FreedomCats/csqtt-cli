package coreproc

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Выдуманные токен VK и хеш звонка, который «создаёт» поддельное ядро.
const (
	fakeVKToken  = "vk1.a.fake-token-0123456789"
	fakeCallHash = "FakeCall_0123-abcdEFGHijklMNOPqrst"
)

// fakeAutoJSConfig — с чем запускается поддельное ядро в auto_js. Хеш
// профиля задан нарочно: в auto_js он ядру не уходит.
var fakeAutoJSConfig = Config{
	Peer:       "203.0.113.10:46000",
	Password:   "p",
	Hashes:     []string{"profile-hash-unused"},
	Workers:    126,
	VKHashMode: VKHashModeAutoJS,
	VKToken:    fakeVKToken,
}

func TestArgsAutoJS(t *testing.T) {
	cfg := fakeAutoJSConfig
	cfg.VKAuthMode = "legacy" // в auto_js не действует
	want := []string{
		"--secrets-stdin",
		"--peer=203.0.113.10:46000",
		"--workers=70",
		"--vk-hash-mode=auto_js",
		"--vk-auth-mode=auto_js",
	}
	if got := cfg.Args(); !slices.Equal(got, want) {
		t.Errorf("Args() = %q, want %q", got, want)
	}
	for _, arg := range cfg.Args() {
		if strings.Contains(arg, fakeVKToken) || strings.Contains(arg, "profile-hash-unused") {
			t.Errorf("секрет в аргументах: %q", arg)
		}
	}
	// Режим с хешами — как был: без режимов auto_js и без предела 70.
	plain := cfg
	plain.VKHashMode = ""
	if got := plain.Args(); slices.ContainsFunc(got, func(a string) bool {
		return strings.Contains(a, "auto_js")
	}) || !slices.Contains(got, "--workers=100") || !slices.Contains(got, "--vk-auth-mode=legacy") {
		t.Errorf("Args() без auto_js = %q", got)
	}
}

func TestNormalizeWorkersAutoJS(t *testing.T) {
	for _, c := range []struct{ requested, want int }{
		{-1, 5}, {0, 5}, {5, 5}, {9, 10}, {10, 10}, {50, 50}, {63, 60}, {70, 70}, {71, 70}, {80, 70}, {100, 70}, {math.MaxInt, 70},
	} {
		if got := NormalizeWorkersAutoJS(c.requested); got != c.want {
			t.Errorf("NormalizeWorkersAutoJS(%d) = %d, want %d", c.requested, got, c.want)
		}
	}
	// Предел 70 — только в auto_js.
	if got := (Config{Workers: 80, VKHashMode: VKHashModeAutoJS}).NormalizedWorkers(); got != 70 {
		t.Errorf("NormalizedWorkers в auto_js = %d, want 70", got)
	}
	if got := (Config{Workers: 80}).NormalizedWorkers(); got != 80 {
		t.Errorf("NormalizedWorkers без auto_js = %d, want 80", got)
	}
}

// decodeLine — json из строки вида <prefix><base64 json>.
func decodeLine(t *testing.T, line, prefix string) map[string]any {
	t.Helper()
	encoded, ok := strings.CutPrefix(line, prefix)
	if !ok {
		t.Fatalf("строка без %s: %q", prefix, line)
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		t.Fatalf("%s: не base64 STANDARD: %v", prefix, err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// В auto_js первая запись — секреты без хешей и токен следом; без auto_js —
// одни секреты, даже если токен задан.
func TestStartInput(t *testing.T) {
	input, err := fakeAutoJSConfig.startInput()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(input, "\n")
	if len(lines) != 3 || lines[2] != "" {
		t.Fatalf("startInput = %q, want две строки с переводом строки", input)
	}
	if got, want := decodeLine(t, lines[0], "SECRETS:"), map[string]any{"password": "p"}; !reflect.DeepEqual(got, want) {
		t.Errorf("секреты = %v, want %v", got, want)
	}
	if got, want := decodeLine(t, lines[1], "VK_JS_BOOTSTRAP:"), map[string]any{"token": fakeVKToken}; !reflect.DeepEqual(got, want) {
		t.Errorf("bootstrap = %v, want %v", got, want)
	}

	plain := fakeAutoJSConfig
	plain.VKHashMode = ""
	if input, err := plain.startInput(); err != nil || input != plain.SecretsLine()+"\n" || strings.Contains(input, "VK_JS_BOOTSTRAP") {
		t.Errorf("startInput без auto_js = %q, %v", input, err)
	}

	for _, token := range []string{"", "  "} {
		noToken := fakeAutoJSConfig
		noToken.VKToken = token
		if _, err := noToken.startInput(); !errors.Is(err, ErrNoVKToken) {
			t.Errorf("startInput с токеном %q: %v, want ErrNoVKToken", token, err)
		}
	}
	long := fakeAutoJSConfig
	long.VKToken = strings.Repeat("x", maxStartInput)
	if _, err := long.startInput(); err == nil || strings.Contains(err.Error(), long.VKToken) {
		t.Errorf("startInput с длинным токеном: %v", err)
	}
}

// recorder — stdin ядра для тестов без процесса: запоминает каждую запись.
type recorder struct {
	mu     sync.Mutex
	writes []string
}

func (r *recorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes = append(r.writes, string(b))
	return len(b), nil
}

func (r *recorder) Close() error { return nil }

func (r *recorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.writes)
}

// Секреты и токен уходят ядру одной записью: иначе строка токена могла бы
// прийти в одном чтении с командами, и ядро выбросило бы их.
func TestStartInputOneWrite(t *testing.T) {
	rec := &recorder{}
	p := &Process{stdin: rec}
	input, err := fakeAutoJSConfig.startInput()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.write(input); err != nil {
		t.Fatal(err)
	}
	if got := rec.got(); len(got) != 1 || got[0] != input {
		t.Errorf("записи = %q", got)
	}
}

// Без процесса: команды остановки не уходят до первой строки ядра и уходят
// после — одной записью, FINISH_VK_CALLS первой.
func TestStopAutoJSWaitsForOutput(t *testing.T) {
	for _, finish := range []bool{false, true} {
		rec := &recorder{}
		p := &Process{stdin: rec, autoJS: true, output: make(chan struct{}),
			processed: make(chan struct{}), done: make(chan struct{})}
		result := make(chan error, 1)
		go func() { result <- p.stopAutoJS(finish, time.Minute, time.Minute) }()
		time.Sleep(100 * time.Millisecond)
		if got := rec.got(); len(got) != 0 {
			t.Fatalf("до первой строки ядру ушло %q", got)
		}
		p.observe(Log{Text: "[П3] Сокеты ядра — через интерфейс 2"})
		want := "STOP\n"
		if finish {
			want = "FINISH_VK_CALLS\nSTOP\n"
		}
		for deadline := time.Now().Add(5 * time.Second); len(rec.got()) == 0; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("после первой строки команды не ушли")
			}
		}
		if got := rec.got(); !slices.Equal(got, []string{want}) {
			t.Errorf("finish=%v: записи = %q, want %q", finish, got, want)
		}
		close(p.done)
		if err := <-result; err != nil {
			t.Errorf("finish=%v: stopAutoJS = %v", finish, err)
		}
	}
}

func TestMaskCallHash(t *testing.T) {
	short := fakeCallHash[:8]
	guest := "https://api.vk.me/method/auth.getAnonymToken?v=5.276&client_id=8093730&link=https%3A%2F%2Fvk.com%2Fcall%2Fjoin%2F" +
		fakeCallHash + "&device_id=00000000-0000-4000-8000-000000000000&anonymName=Test&lang=en"
	guestMasked := "https://api.vk.me/method/auth.getAnonymToken?v=5.276&client_id=8093730&link=https%3A%2F%2Fvk.com%2Fcall%2Fjoin%2F" +
		"<хеш>&device_id=00000000-0000-4000-8000-000000000000&anonymName=Test&lang=en"
	cases := map[string]string{
		// worker.rs: начало хеша при запросе кредов.
		"[КРЕД #1] Запрос (хеш: " + short + "...)": "[КРЕД #1] Запрос (хеш: <хеш>...)",
		// auth.rs: адрес гостевого пути в тексте ошибки HTTP-клиента.
		"[VK JS] Переход на Авто #2 не выполнен: error sending request for url (" + guest +
			"): client error (Connect): tcp connect error: Network is unreachable (os error 101)": "[VK JS] Переход на Авто #2 не выполнен: error sending request for url (" + guestMasked +
			"): client error (Connect): tcp connect error: Network is unreachable (os error 101)",
		"[КРЕД #2] Ошибка: Auto JS: Auto JS session #2: messages.getCallToken: HTTP-запрос: timeout; fallback Авто: error sending request for url (" +
			guest + "). Повторяем через 1.2s...": "[КРЕД #2] Ошибка: Auto JS: Auto JS session #2: messages.getCallToken: HTTP-запрос: timeout; fallback Авто: error sending request for url (" +
			guestMasked + "). Повторяем через 1.2s...",
		// Ссылка как есть и строчными знаками кодировки.
		"vk_join_link=https://vk.com/call/join/" + fakeCallHash + "&name=x": "vk_join_link=https://vk.com/call/join/<хеш>&name=x",
		"link=https%3a%2f%2fvk.ru%2fcall%2fjoin%2f" + fakeCallHash:          "link=https%3a%2f%2fvk.ru%2fcall%2fjoin%2f<хеш>",
		"https://vk.ru/call/join/" + fakeCallHash + "/":                     "https://vk.ru/call/join/<хеш>/",
		// auth.rs, шаг 5 гостевого пути: голый хеш в параметре joinLink.
		"[VK JS] Переход на Авто #3 не выполнен: error sending request for url (https://calls.okcdn.ru/fb.do?joinLink=" +
			fakeCallHash + "&isVideo=false&protocolVersion=5&method=vchat.joinConversationByLink&format=JSON): operation timed out": "[VK JS] Переход на Авто #3 не выполнен: error sending request for url (https://calls.okcdn.ru/fb.do?joinLink=" +
			"<хеш>&isVideo=false&protocolVersion=5&method=vchat.joinConversationByLink&format=JSON): operation timed out",
		// Без хеша — как было.
		"[VK JS] Звонок создан":             "[VK JS] Звонок создан",
		"call/join/ и (хеш: )":              "call/join/ и (хеш: )",
		"[КРЕД #1] OK, TURN: [\"x\"], до 9": "[КРЕД #1] OK, TURN: [\"x\"], до 9",
	}
	for in, want := range cases {
		got := maskCallHash(in)
		if got != want {
			t.Errorf("maskCallHash(%q) =\n%q\nwant\n%q", in, got, want)
		}
		if strings.Contains(got, short) {
			t.Errorf("хеш остался: %q", got)
		}
		if again := maskCallHash(got); again != got {
			t.Errorf("повторная маскировка меняет строку: %q → %q", got, again)
		}
	}
}

// В auto_js хеш звонка прячется и в тексте события DTLS: в отказе — текст
// ошибки ядра.
func TestParseAutoJSMasksDTLS(t *testing.T) {
	line := `__CSQTT_EVENT__|DTLS|{"message":"рукопожатие не состоялось (https://vk.ru/call/join/` +
		fakeCallHash + `)","worker":4}`
	want := DTLSEvent{Worker: 4, Message: "рукопожатие не состоялось (https://vk.ru/call/join/<хеш>)"}
	if got := parseAutoJS(ParseLine)(line); got != want {
		t.Errorf("parseAutoJS(%q) =\n%#v\nwant\n%#v", line, got, want)
	}
}

func TestMaskStdinSecrets(t *testing.T) {
	input, err := fakeAutoJSConfig.startInput()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(input, "\n")
	for _, line := range lines[:2] {
		got := maskLog("[STDIN] " + line)
		prefix, _, _ := strings.Cut(line, ":")
		if want := "[STDIN] " + prefix + ":<скрыт, " + strconv.Itoa(len(line)-len(prefix)-1) + " зн.>"; got != want {
			t.Errorf("maskLog(%q) = %q, want %q", line, got, want)
		}
	}
	for _, line := range []string{
		"[КЛИЕНТ] Тайм-аут передачи секретов: нет строки SECRETS за 15 с",
		"[ФАТАЛ] [КЛИЕНТ] Первая строка stdin — не SECRETS:",
		"[ФАТАЛ] неверный формат данных Auto JS",
	} {
		if got := maskLog(line); got != line {
			t.Errorf("maskLog(%q) = %q", line, got)
		}
	}
}

func TestParseFatal(t *testing.T) {
	cases := []struct {
		line   string
		want   FatalError
		reject bool
	}{
		// Первый запрос с токеном (messages.getCallToken): ошибка API без
		// приписки метода.
		{"[ФАТАЛ] API 5: User authorization failed: invalid access_token (4).",
			FatalError{Kind: FatalAPI, APICode: 5}, true},
		{"[ФАТАЛ] API 27: Group authorization failed: method is unavailable with group auth.",
			FatalError{Kind: FatalAPI, APICode: 27}, true},
		{"[ФАТАЛ] API 28: Application authorization failed: method is unavailable with service token.",
			FatalError{Kind: FatalAPI, APICode: 28}, true},
		{"[ФАТАЛ] API 4: Incorrect signature", FatalError{Kind: FatalAPI, APICode: 4}, false},
		{"[ФАТАЛ] API 18: User was deleted or banned", FatalError{Kind: FatalAPI, APICode: 18}, false},
		// auth.anonymLogin и system.getInfo OK Calls — тоже без приписки.
		{"[ФАТАЛ] API 102: PARAM_SESSION_EXPIRED : Session expired", FatalError{Kind: FatalAPI, APICode: 102}, false},
		{"[ФАТАЛ] API unknown: API error", FatalError{Kind: FatalAPI}, false},
		// Создание звонка — с припиской, и код 5 тут не про токен.
		{"[ФАТАЛ] vchat.startConversation: API 5: forbidden",
			FatalError{Kind: FatalAPI, Method: "vchat.startConversation", APICode: 5}, false},
		{"[ФАТАЛ] vchat.startConversation: нет join_link", FatalError{Kind: FatalOther}, false},
		// Сеть и HTTP.
		{"[ФАТАЛ] messages.getCallToken: HTTP-запрос: error sending request for url (https://api.vk.ru/method/messages.getCallToken?v=5.285&client_id=6287487): client error (Connect): dns error: failed to lookup address information",
			FatalError{Kind: FatalNetwork}, false},
		{"[ФАТАЛ] messages.getCallToken: HTTP 502 Bad Gateway", FatalError{Kind: FatalNetwork}, false},
		{"[ФАТАЛ] auth.anonymLogin: HTTP 429 Too Many Requests", FatalError{Kind: FatalNetwork}, false},
		{"[ФАТАЛ] system.getInfo: чтение HTTP-ответа: error decoding response body", FatalError{Kind: FatalNetwork}, false},
		{"[ФАТАЛ] auth.anonymLogin: некорректный JSON-ответ: expected value at line 1 column 1", FatalError{Kind: FatalNetwork}, false},
		{"[ФАТАЛ] vchat.startConversation: слишком большой HTTP-ответ", FatalError{Kind: FatalNetwork}, false},
		// Имя сервера — до VK, не сеть до VK.
		{"[ФАТАЛ] ошибка разбора пира: пустой DNS-ответ для пира", FatalError{Kind: FatalPeer}, false},
		// То, что ядру дала оболочка.
		{"[ФАТАЛ] тайм-аут передачи данных Auto JS: deadline has elapsed", FatalError{Kind: FatalClient}, false},
		{"[ФАТАЛ] некорректные данные Auto JS: missing field `token`", FatalError{Kind: FatalClient}, false},
		{"[ФАТАЛ] слишком большие данные Auto JS", FatalError{Kind: FatalClient}, false},
		{"[ФАТАЛ] [П3] -bind-if 7: нет интерфейса с таким индексом", FatalError{Kind: FatalClient}, false},
		{"[ФАТАЛ] [КЛИЕНТ] Нужен -password: WRAP ключ выводится из пароля подключения", FatalError{Kind: FatalClient}, false},
		{"[ФАТАЛ] [КЛИЕНТ] Первая строка stdin — не SECRETS:", FatalError{Kind: FatalClient}, false},
		// Прочее.
		{"[ФАТАЛ] VK access token отсутствует", FatalError{Kind: FatalOther}, false},
		{"[ФАТАЛ] небезопасный api_base_url: host=example.com, path=/", FatalError{Kind: FatalOther}, false},
		{"[ФАТАЛ] messages.getCallToken: нет response", FatalError{Kind: FatalOther}, false},
	}
	for _, c := range cases {
		got, ok := ParseFatal(c.line)
		if !ok {
			t.Errorf("ParseFatal(%q): не разобрана", c.line)
			continue
		}
		c.want.Text = strings.TrimPrefix(c.line, "[ФАТАЛ] ")
		if *got != c.want {
			t.Errorf("ParseFatal(%q) = %+v, want %+v", c.line, *got, c.want)
		}
		if got.TokenRejected() != c.reject {
			t.Errorf("ParseFatal(%q).TokenRejected() = %v", c.line, got.TokenRejected())
		}
		if got.Error() != c.want.Text {
			t.Errorf("Error() = %q", got.Error())
		}
	}
	for _, line := range []string{"[VK JS] Звонок не создан: API 5: x", "ФАТАЛ API 5: x", ""} {
		if _, ok := ParseFatal(line); ok {
			t.Errorf("ParseFatal(%q) разобрала не фатальную строку", line)
		}
	}
}

// Временные ошибки API (коды 1, 6, 10) — с припиской метода и без; отказ
// токена, flood control и не ошибки API — нет.
func TestFatalTransient(t *testing.T) {
	for line, want := range map[string]bool{
		"[ФАТАЛ] API 1: Unknown error occurred":                                true,
		"[ФАТАЛ] vchat.startConversation: API 6: Too many requests per second": true,
		"[ФАТАЛ] API 10: Internal server error":                                true,
		"[ФАТАЛ] vchat.startConversation: API 9: Flood control":                false,
		"[ФАТАЛ] API 5: User authorization failed: invalid access_token (4).":  false,
		"[ФАТАЛ] API unknown: API error":                                       false,
		"[ФАТАЛ] auth.anonymLogin: HTTP 429 Too Many Requests":                 false,
		"[ФАТАЛ] vchat.startConversation: нет join_link":                       false,
	} {
		got, ok := ParseFatal(line)
		if !ok {
			t.Fatalf("ParseFatal(%q): не разобрана", line)
		}
		if got.Transient() != want {
			t.Errorf("ParseFatal(%q).Transient() = %v", line, got.Transient())
		}
	}
}

func TestCallState(t *testing.T) {
	for _, c := range []struct {
		lines         []string
		finish, plain bool // unclean для StopFinishingCalls и Stop
	}{
		{nil, false, false},
		{[]string{"[VK JS] Звонок не создан: API 9: Flood control"}, false, false},
		{[]string{"[VK JS] Звонок создан"}, true, true},
		{[]string{"[VK JS] Звонок создан", "[VK JS] Звонки завершены 1/1"}, false, true},
		{[]string{"[VK JS] Звонок создан", "[VK JS] Звонки завершены 0/1"}, true, true},
		// Создатель вышел — звонок закрыт, и при finish тоже.
		{[]string{"[VK JS] Звонок создан", "[VK JS] Создатель вышел из звонков 1/1"}, false, false},
		{[]string{"[VK JS] Звонок создан", "[VK JS] Создатель вышел из звонков 0/1"}, true, true},
		{[]string{"[VK JS] Звонок создан", "[VK JS] Звонки завершены 1/1 (x2)"}, false, true},
		{[]string{"[VK JS] Звонок создан", "[VK JS] Звонки завершены "}, true, true},
		{[]string{"[VK JS] Звонок создан", "[VK JS] Звонки завершены 0/0"}, true, true},
		// Ядро 2.5.0: создатель выходит ещё при работе, и forceFinish при
		// остановке звонка уже не находит.
		{[]string{"[VK JS] Звонок создан", "[VK JS] Создатель вышел из звонков 1/1",
			"[VK JS] Звонки завершены 0/1"}, false, false},
		{[]string{"[VK JS] Звонок создан", "[VK JS] Создатель вышел из звонков 0/1",
			"[VK JS] Звонки завершены 0/1"}, true, true},
		{[]string{"[VK JS] Звонок создан", "[VK JS] Создатель вышел из звонков 0/1",
			"[VK JS] Звонки завершены 1/1"}, false, true},
		// Без FINISH_VK_CALLS 2.5.0 выводит создателя второй раз — из уже
		// закрытого звонка: поздний неуспех первый итог не отменяет.
		{[]string{"[VK JS] Звонок создан", "[VK JS] Создатель вышел из звонков 1/1",
			"[VK JS] Создатель вышел из звонков 0/1"}, false, false},
		{[]string{"[VK JS] Звонок создан", "[VK JS] Создатель вышел из звонков 0/1",
			"[VK JS] Создатель вышел из звонков 1/1"}, false, false},
	} {
		var s callState
		for _, l := range c.lines {
			s.observe(l)
		}
		if s.unclean(true) != c.finish || s.unclean(false) != c.plain {
			t.Errorf("%q: unclean(finish) = %v, unclean(stop) = %v; want %v, %v",
				c.lines, s.unclean(true), s.unclean(false), c.finish, c.plain)
		}
	}
}

// Сторож разбора: строки, по которым оболочка узнаёт отказ, хеш звонка и
// итог уборки, ядро пишет по-прежнему. Новая версия ядра с другими словами
// должна уронить тест при обновлении, а не молча сломать разбор.
func TestAutoJSStringsInCore(t *testing.T) {
	want := map[string][]string{
		"main.rs": {
			`crate::log_error!("[ФАТАЛ] {error:#}")`,
			`.context("ошибка разбора пира")`,
			"let peer = resolve_peer(&arguments.peer).await?;",
			`.context("тайм-аут передачи данных Auto JS")?`,
			`.context("чтение данных Auto JS")?`,
			`.context("неверный формат данных Auto JS")?`,
			`bail!("слишком большие данные Auto JS")`,
			`.context("повреждены данные Auto JS")?`,
			`.context("некорректные данные Auto JS")?`,
			`bail!("[КЛИЕНТ] Нужны -peer и хеши VK")`,
			`.strip_prefix("VK_JS_BOOTSTRAP:")`,
			`"FINISH_VK_CALLS" => finish_js_calls.store(true`,
			`line != "FINISH_VK_CALLS"`,
		},
		"secrets_stdin.rs": {`bail!("[КЛИЕНТ] stdin закрыт до строки SECRETS")`},
		"bind_if.rs":       {`anyhow!("[П3] -bind-if {index}: {}"`},
		"vk_js_calls.rs": {
			`bail!("API {code}: {message}")`,
			`.context("HTTP-запрос")`,
			`.context("чтение HTTP-ответа")`,
			`bail!("слишком большой HTTP-ответ")`,
			`bail!("HTTP {status}")`,
			`.context("некорректный JSON-ответ")`,
			// Ошибка API первого запроса с токеном — без приписки метода.
			".context(\"messages.getCallToken\")?;\n    check_api_error(&token_response)?;",
			`"[VK JS] Звонок создан"`,
			`"[VK JS] Звонки завершены {finished}/{total}"`,
			`"[VK JS] Создатель вышел из звонков {finished}/{total}"`,
		},
		"worker.rs": {
			`"[КРЕД #{}] Запрос (хеш: {short_hash}...)"`,
			`"[КРЕД #{}] Ошибка: {error:#}. Повторяем через {delay:?}...",`,
		},
		"auth.rs": {
			`query_escape(&format!("https://vk.ru/call/join/{link}"))`,
			`fb.do?joinLink={}&`,
			`const HEX: &[u8; 16] = b"0123456789ABCDEF";`,
			// Ключи гостевого пути в адресах (guestKeyParams): шаги 2–3,
			// шаг 5 и проверка хешей. Новый ключ в адресе — дополнить список.
			"&anonymous_token={anonymous_token_encoded}&",
			"&secret={}&lang=en\",\n            query_escape(secret)",
			"&anonymToken={}&method=vchat.joinConversationByLink",
			"&session_key={}\",\n            query_escape(link),",
			"&anonymous_token={anonymous_token}&",
			// Сам HTTP-клиент: адрес попадает в текст ошибки.
			"let response = client.post(endpoint).headers(headers).send().await?;",
		},
	}
	for file, parts := range want {
		src := coreSource(t, filepath.Join("..", "..", "core", "rust-client", file))
		for _, part := range parts {
			if !strings.Contains(src, part) {
				t.Errorf("в %s нет %q — разбор auto_js сверить с ядром заново", file, part)
			}
		}
	}
}

// coreSource — исходник ядра с концами строк LF: git на Windows выкладывает
// core/ с CRLF, а часть образцов сторожа многострочная.
func coreSource(t *testing.T, path string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("исходник ядра: %v", err)
	}
	return strings.ReplaceAll(string(src), "\r\n", "\n")
}

func TestCoreSourceCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.rs")
	if err := os.WriteFile(path, []byte("a(\r\n    b)\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := coreSource(t, path); got != "a(\n    b)\n" {
		t.Errorf("coreSource = %q", got)
	}
}

// fakeAutoJSCore — ядро в auto_js (режим autojs поддельного ядра). stdin оно
// читает как настоящее: секреты — одним чтением из канала, токен — из того,
// что пришло тем же чтением, а остаток этого чтения выбрасывает. Команды
// читает только после PROCESS.
//
// Поведение задают переменные окружения: CSQTT_FAKE_READ_DELAY — пауза до
// чтения секретов, CSQTT_FAKE_CREATE_DELAY — «создание звонка» до PROCESS,
// CSQTT_FAKE_CLEANUP_DELAY — уборка после STOP (всё в мс), CSQTT_FAKE_HANG —
// PROCESS не наступает никогда, CSQTT_FAKE_FINISHED — итог завершения
// звонка (по умолчанию 1/1), CSQTT_FAKE_LEFT — итог выхода создателя при
// остановке без FINISH_VK_CALLS (по умолчанию 1/1). CSQTT_FAKE_CREATOR_LEFT —
// итог выхода создателя ещё при работе, как у ядра 2.5.0 (по умолчанию
// создатель держит звонок до остановки, как у 2.1.9).
func fakeAutoJSCore() int {
	signal.Ignore(syscall.SIGPIPE)
	delay := func(name string) {
		ms, _ := strconv.Atoi(os.Getenv(name))
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	fatal := func(text string) int {
		fmt.Fprintln(os.Stderr, "[ФАТАЛ] "+text)
		return 1
	}
	for _, arg := range []string{"--secrets-stdin", "--vk-hash-mode=auto_js", "--vk-auth-mode=auto_js",
		"--workers=70"} {
		if !slices.Contains(os.Args, arg) {
			return fatal("нет аргумента " + arg)
		}
	}

	delay("CSQTT_FAKE_READ_DELAY")
	buf := make([]byte, 8192)
	n, _ := os.Stdin.Read(buf)
	lines := strings.SplitN(string(buf[:n]), "\n", 3)
	if len(lines) < 3 {
		return fatal("тайм-аут передачи данных Auto JS")
	}
	if lines[0] != fakeAutoJSConfig.SecretsLine() {
		return fatal("первая строка stdin — не секреты")
	}
	input, _ := fakeAutoJSConfig.startInput()
	if lines[0]+"\n"+lines[1]+"\n" != input {
		return fatal("неверный формат данных Auto JS")
	}
	// lines[2] — остаток чтения: читатель токена выбрасывает его.

	fmt.Fprintln(os.Stderr, "[VK JS] Сессия Calls подтверждена")
	if os.Getenv("CSQTT_FAKE_HANG") != "" {
		for {
			time.Sleep(time.Hour)
		}
	}
	delay("CSQTT_FAKE_CREATE_DELAY")
	fmt.Fprintln(os.Stderr, "[VK JS] Звонок создан")
	fmt.Printf("__CSQTT_EVENT__|PROCESS|{\"pid\":%d}\n", os.Getpid())
	if left := os.Getenv("CSQTT_FAKE_CREATOR_LEFT"); left != "" {
		fmt.Fprintln(os.Stderr, "[VK JS] TURN-боты готовы, создатель выходит из звонка")
		fmt.Fprintln(os.Stderr, "[VK JS] Создатель вышел из звонков "+left)
	} else {
		fmt.Fprintln(os.Stderr, "[VK JS] Создатель удерживает звонок")
	}
	fmt.Fprintln(os.Stderr, "[КРЕД #1] Запрос (хеш: "+fakeCallHash[:8]+"...)")
	fmt.Fprintln(os.Stderr, "[VK JS] Переход на Авто #1 не выполнен: error sending request for url "+
		"(https://api.vk.me/method/auth.getAnonymToken?v=5.276&link=https%3A%2F%2Fvk.com%2Fcall%2Fjoin%2F"+fakeCallHash+"&lang=en)")
	fmt.Printf("__CSQTT_EVENT__|CALL_UNAVAILABLE|{\"code\":954,\"hash\":%q}\n", fakeCallHash)

	in := bufio.NewScanner(os.Stdin)
	finish := false
	for in.Scan() {
		line := in.Text()
		if line == CmdFinishCalls {
			finish = true // ядро её в журнал не повторяет
			continue
		}
		fmt.Fprintln(os.Stderr, "[STDIN] "+line)
		if line == CmdStop {
			break
		}
	}
	delay("CSQTT_FAKE_CLEANUP_DELAY")
	if finish {
		result := os.Getenv("CSQTT_FAKE_FINISHED")
		if result == "" {
			result = "1/1"
		}
		fmt.Fprintln(os.Stderr, "[VK JS] Звонки завершены "+result)
	} else {
		result := os.Getenv("CSQTT_FAKE_LEFT")
		if result == "" {
			result = "1/1"
		}
		fmt.Fprintln(os.Stderr, "[VK JS] Создатель вышел из звонков "+result)
	}
	fmt.Println(`__CSQTT_EVENT__|STOPPED|{}`)
	fmt.Fprintln(os.Stderr, "[КЛИЕНТ] Все воркеры завершены")
	return 0
}

// startAutoJS запускает поддельное ядро в auto_js с переменными env. Под
// -race тестовый бинарь при выходе спит секунду (atexit_sleep_ms) — у
// поддельного ядра это съело бы срок уборки.
func startAutoJS(t *testing.T, env ...string) *Process {
	t.Helper()
	env = append([]string{fakeCoreEnv + "=autojs", "GORACE=atexit_sleep_ms=0"}, env...)
	p, err := startConfig(os.Args[0], fakeAutoJSConfig, env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.stop(0) })
	return p
}

// logTexts — строки журнала из сообщений.
func logTexts(msgs []Message) []string {
	var logs []string
	for _, m := range msgs {
		if l, ok := m.(Log); ok {
			logs = append(logs, l.Text)
		}
	}
	return logs
}

// Ядро читает секреты не сразу, а остановку просят в тот же миг: команды
// уходят только после первой строки ядра, и звонок завершается.
func TestStopAutoJSBeforeCoreReadsSecrets(t *testing.T) {
	for _, finish := range []bool{true, false} {
		p := startAutoJS(t, "CSQTT_FAKE_READ_DELAY=300", "CSQTT_FAKE_CREATE_DELAY=200")
		msgs, _, drained := collect(p, func(Message) bool { return false })
		if err := p.stopAutoJS(finish, 10*time.Second, 10*time.Second); err != nil {
			t.Fatalf("finish=%v: stopAutoJS = %v", finish, err)
		}
		waitFor(t, drained, "закрытие Messages")
		logs := logTexts(msgs())
		want := "[VK JS] Создатель вышел из звонков 1/1"
		if finish {
			want = "[VK JS] Звонки завершены 1/1"
		}
		if !slices.Contains(logs, "[STDIN] STOP") || !slices.Contains(logs, want) {
			t.Errorf("finish=%v: журнал = %q", finish, logs)
		}
		if !slices.Contains(msgs(), Message(StoppedEvent{})) {
			t.Errorf("finish=%v: нет STOPPED", finish)
		}
	}
}

// Обратная проверка поддельного ядра: те же команды, записанные сразу, без
// ожидания первой строки, ядро выбрасывает вместе со строкой токена.
func TestAutoJSCommandsBeforeSecretsAreLost(t *testing.T) {
	p := startAutoJS(t, "CSQTT_FAKE_READ_DELAY=300")
	msgs, seen, _ := collect(p, func(m Message) bool { _, ok := m.(PIDEvent); return ok })
	if err := p.write(CmdFinishCalls + "\n" + CmdStop + "\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, seen, "PROCESS")
	time.Sleep(500 * time.Millisecond)
	select {
	case <-p.done:
		t.Fatalf("ядро вышло по командам, пришедшим до чтения секретов: %q", logTexts(msgs()))
	default:
	}
	var left *CallLeftError
	if err := p.kill(true); !errors.As(err, &left) || !left.Killed {
		t.Errorf("kill = %v, want *CallLeftError с Killed", err)
	}
}

func TestStopAutoJSTimeouts(t *testing.T) {
	const short = 300 * time.Millisecond
	cases := []struct {
		name             string
		env              []string
		process, cleanup time.Duration
		killed           bool
		min, max         time.Duration
	}{
		// Звонок «создаётся» вечно: до PROCESS — processTimeout.
		{"до PROCESS", []string{"CSQTT_FAKE_HANG=1"}, short, time.Minute, true, short, 5 * time.Second},
		// Ядро молчит, не прочтя секретов: команды так и не ушли.
		{"без вывода", []string{"CSQTT_FAKE_READ_DELAY=60000"}, short, time.Minute, true, short, 5 * time.Second},
		// Уборка дольше срока.
		{"уборка", []string{"CSQTT_FAKE_CLEANUP_DELAY=60000"}, time.Minute, short, true, short, 5 * time.Second},
		// Звонок «создаётся» дольше уборочного срока, но в срок PROCESS.
		{"долгое создание", []string{"CSQTT_FAKE_CREATE_DELAY=1500"}, 10 * time.Second, time.Second, false, 1500 * time.Millisecond, 5 * time.Second},
		// Ядро вышло само, а звонок не завершён.
		{"0/1", []string{"CSQTT_FAKE_FINISHED=0/1"}, 10 * time.Second, 10 * time.Second, false, 0, 5 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := startAutoJS(t, c.env...)
			_, _, drained := collect(p, func(Message) bool { return false })
			start := time.Now()
			err := p.stopAutoJS(true, c.process, c.cleanup)
			d := time.Since(start)
			waitFor(t, drained, "закрытие Messages")
			if d < c.min || d > c.max {
				t.Errorf("остановка заняла %v, want от %v до %v", d, c.min, c.max)
			}
			var left *CallLeftError
			switch {
			case c.name == "долгое создание":
				if err != nil {
					t.Errorf("stopAutoJS = %v, want nil", err)
				}
			case !errors.As(err, &left) || left.Killed != c.killed:
				t.Errorf("stopAutoJS = %v, want *CallLeftError с Killed=%v", err, c.killed)
			case c.killed && left.Err == nil:
				t.Error("у убитого ядра нет ошибки выхода")
			}
		})
	}
}

// Ядро 2.5.0: создатель вышел из звонка ещё при работе, и VK звонок закрыл.
// При остановке forceFinish его уже не находит (0/1), а повторный выход
// создателя без FINISH_VK_CALLS не удаётся (0/1) — уборка всё равно
// подтверждена. Создатель не вышел — звонок мог остаться.
func TestStopAutoJSCreatorLeft(t *testing.T) {
	for _, c := range []struct {
		name   string
		finish bool
		env    []string
		left   bool // *CallLeftError
	}{
		{"finish", true, []string{"CSQTT_FAKE_CREATOR_LEFT=1/1", "CSQTT_FAKE_FINISHED=0/1"}, false},
		{"stop", false, []string{"CSQTT_FAKE_CREATOR_LEFT=1/1", "CSQTT_FAKE_LEFT=0/1"}, false},
		{"finish, не вышел", true, []string{"CSQTT_FAKE_CREATOR_LEFT=0/1", "CSQTT_FAKE_FINISHED=0/1"}, true},
		{"stop, не вышел", false, []string{"CSQTT_FAKE_CREATOR_LEFT=0/1", "CSQTT_FAKE_LEFT=0/1"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := startAutoJS(t, c.env...)
			_, _, drained := collect(p, func(Message) bool { return false })
			err := p.stopAutoJS(c.finish, 10*time.Second, 10*time.Second)
			waitFor(t, drained, "закрытие Messages")
			var left *CallLeftError
			switch {
			case !c.left && err != nil:
				t.Errorf("stopAutoJS = %v, want nil", err)
			case c.left && (!errors.As(err, &left) || left.Killed):
				t.Errorf("stopAutoJS = %v, want *CallLeftError без Killed", err)
			}
		})
	}
}

// Ни токен, ни хеш созданного звонка в вывод ядра не попадают.
func TestAutoJSOutputHidesSecrets(t *testing.T) {
	p := startAutoJS(t)
	msgs, seen, drained := collect(p, func(m Message) bool { _, ok := m.(CallUnavailableEvent); return ok })
	waitFor(t, seen, "CALL_UNAVAILABLE")
	if err := p.StopFinishingCalls(); err != nil {
		t.Fatalf("StopFinishingCalls = %v", err)
	}
	waitFor(t, drained, "закрытие Messages")
	input, _ := fakeAutoJSConfig.startInput()
	secrets := []string{fakeVKToken, fakeCallHash, fakeCallHash[:8], strings.Split(input, "\n")[1][len("VK_JS_BOOTSTRAP:"):]}
	var masked int
	for _, m := range msgs() {
		text := fmt.Sprintf("%+v", m)
		for _, s := range secrets {
			if strings.Contains(text, s) {
				t.Errorf("в выводе секрет %q: %s", s, text)
			}
		}
		if strings.Contains(text, "<хеш>") {
			masked++
		}
		if e, ok := m.(CallUnavailableEvent); ok && (e.Hash != "" || e.Code != 954) {
			t.Errorf("CALL_UNAVAILABLE = %+v", e)
		}
	}
	if masked != 2 {
		t.Errorf("строк со спрятанным хешем %d, want 2: %q", masked, logTexts(msgs()))
	}
}

// Ядро в auto_js без токена не запускается.
func TestStartAutoJSWithoutToken(t *testing.T) {
	cfg := fakeAutoJSConfig
	cfg.VKToken = ""
	if _, err := Start(os.Args[0]+".нет", cfg); !errors.Is(err, ErrNoVKToken) {
		t.Errorf("Start = %v, want ErrNoVKToken", err)
	}
}

// Токен ядру не в auto_js не уходит: ядро повторило бы его в журнал.
func TestNoBootstrapWithoutAutoJS(t *testing.T) {
	cfg := fakeConfig
	cfg.VKToken = fakeVKToken
	p, err := startConfig(os.Args[0], cfg, []string{fakeCoreEnv + "=replay"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.stop(0) })
	msgs, _, drained := collect(p, func(Message) bool { return false })
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
	waitFor(t, drained, "закрытие Messages")
	logs := logTexts(msgs())
	if !slices.Contains(logs, "[STDIN] STOP") || slices.ContainsFunc(logs, func(l string) bool {
		return strings.Contains(l, "VK_JS_BOOTSTRAP") || strings.Contains(l, fakeVKToken)
	}) {
		t.Errorf("журнал = %q", logs)
	}
}

// Kill прерывает идущую остановку: ядро, которое создаёт звонок вечно, убито
// сразу, а не через StopProcessTimeoutAutoJS, и звонок назван оставшимся.
// Вышедшее ядро Kill не трогает.
func TestKillInterruptsStop(t *testing.T) {
	p := startAutoJS(t, "CSQTT_FAKE_HANG=1")
	_, _, drained := collect(p, func(Message) bool { return false })
	stopped := make(chan error, 1)
	go func() { stopped <- p.StopFinishingCalls() }()
	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-stopped:
		t.Fatalf("остановка кончилась до Kill: %v", err)
	default:
	}
	start := time.Now()
	p.Kill()
	var err error
	select {
	case err = <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("остановка не вернулась после Kill")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("после Kill остановка шла ещё %v", d)
	}
	var left *CallLeftError
	if !errors.As(err, &left) || !left.Killed {
		t.Errorf("StopFinishingCalls = %v, want *CallLeftError с Killed", err)
	}
	waitFor(t, drained, "закрытие Messages")
	p.Kill()
}
