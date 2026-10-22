package coreproc

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestMaskLog(t *testing.T) {
	cases := map[string]string{
		"[STDIN] CAPTCHA_RESULT|abcdef":             "[STDIN] CAPTCHA_RESULT|<токен скрыт, 6 зн.>",
		"[STDIN] CAPTCHA_RESULT|abcdef (x2)":        "[STDIN] CAPTCHA_RESULT|<токен скрыт, 6 зн.> (x2)",
		"[STDIN] CAPTCHA_RESULT|error:closed":       "[STDIN] CAPTCHA_RESULT|error:closed",
		"[STDIN] CAPTCHA_RESULT|":                   "[STDIN] CAPTCHA_RESULT|",
		"[STDIN] PAUSE":                             "[STDIN] PAUSE",
		"CAPTCHA_RESULT|one CAPTCHA_RESULT|two-two": "CAPTCHA_RESULT|<токен скрыт, 3 зн.> CAPTCHA_RESULT|<токен скрыт, 7 зн.>",
		"[КАПЧА] Результат от Kotlin записан в канал":      "[КАПЧА] Результат от Kotlin записан в канал",
		"[STDIN] CAPTCHA_RESULT|tok.en_-with=signs\tхвост": "[STDIN] CAPTCHA_RESULT|<токен скрыт, 18 зн.>\tхвост",
		// Адрес капчи с токеном сессии (captcha.rs, solveOnce).
		"[КАПЧА] solveOnce URL: https://id.vk.ru/not_robot_captcha?domain=vk.com&session_token=abc.DEF-1&variant=popup": "[КАПЧА] solveOnce URL: https://id.vk.ru/not_robot_captcha?domain=vk.com&session_token=<скрыт, 9 зн.>&variant=popup",
		"session_token=abc":                       "session_token=<скрыт, 3 зн.>",
		"session_token=a#x session_token=bb (x2)": "session_token=<скрыт, 1 зн.>#x session_token=<скрыт, 2 зн.> (x2)",
		"session_token=&next=1":                   "session_token=&next=1",
	}
	for in, want := range cases {
		if got := maskLog(in); got != want {
			t.Errorf("maskLog(%q) = %q, want %q", in, got, want)
		}
	}
	// То же в stdout: неразобранная строка уходит в журнал скрытой.
	if got := ParseLine("[STDIN] CAPTCHA_RESULT|abcdef"); got != (Log{Text: "[STDIN] CAPTCHA_RESULT|<токен скрыт, 6 зн.>"}) {
		t.Errorf("ParseLine = %#v", got)
	}
}

// Выдуманные временные ключи гостевого пути VK Calls (auth.rs,
// vk_calls_path) — в том виде, в каком они стоят в адресах: anonymous_token и
// secret закодированы (query_escape), anonymToken и session_key шага 5 — как
// есть.
const (
	fakeAnonToken  = "fakeAnon.0123-abcd_EFGH"
	fakeSecret     = "fakeSecret%2B%2F%3D"
	fakeOKToken    = "$fakeOKtoken.0123-abcd"
	fakeSessionKey = "fake-sess.key_0123"
)

// guestURLs — адреса шагов 2, 3 и 5 гостевого пути с ключами keys
// (anonymous_token, secret, anonymToken, session_key) и хешем звонка hash.
func guestURLs(hash string, keys [4]string) (step2, step3, step5 string) {
	link := "https%3A%2F%2Fvk.com%2Fcall%2Fjoin%2F" + hash
	dev := "&device_id=00000000-0000-4000-8000-000000000000"
	step2 = "https://api.vk.me/method/messages.getCallPreview?v=5.276&anonymous_token=" + keys[0] + dev +
		"&extended=1&fields=first_name,last_name,photo_200&lang=en&link=" + link
	step3 = "https://api.vk.me/method/messages.getAnonymCallToken?v=5.276&anonymous_token=" + keys[0] + dev +
		"&link=" + link + "&name=Test+Name&user_id=123&secret=" + keys[1] + "&lang=en"
	step5 = "https://calls.okcdn.ru/fb.do?joinLink=" + hash + "&isVideo=false&protocolVersion=5&anonymToken=" + keys[2] +
		"&method=vchat.joinConversationByLink&format=JSON&application_key=CGMMEJLGDIHBABABA&session_key=" + keys[3]
	return step2, step3, step5
}

var (
	fakeGuestKeys   = [4]string{fakeAnonToken, fakeSecret, fakeOKToken, fakeSessionKey}
	maskedGuestKeys = [4]string{"<скрыто>", "<скрыто>", "<скрыто>", "<скрыто>"}
)

// guestErrorLines — строки журнала ядра с ошибкой HTTP-клиента на адресе
// гостевого пути (worker.rs, auth.rs): в режиме с хешами и в auto_js.
func guestErrorLines(hash string, keys [4]string) []string {
	step2, step3, step5 := guestURLs(hash, keys)
	return []string{
		"[КРЕД #1] Ошибка: error sending request for url (" + step2 +
			"): client error (Connect): tcp connect error: Network is unreachable (os error 101). Повторяем через 1.2s...",
		"[КРЕД #2] Ошибка: error sending request for url (" + step3 + "): operation timed out. Повторяем через 2s...",
		"[КРЕД #3] Ошибка: error sending request for url (" + step5 + "). Повторяем через 1.5s...",
		"[VK JS] Переход на Авто #2 не выполнен: error sending request for url (" + step5 +
			"): client error (SendRequest): connection closed before message completed",
		"[КРЕД #2] Ошибка: Auto JS: Auto JS session #2: messages.getCallToken: HTTP-запрос: timeout; fallback Авто: error sending request for url (" +
			step3 + "): operation timed out. Повторяем через 1.2s...",
	}
}

func TestMaskGuestKeys(t *testing.T) {
	keys := fakeGuestKeys[:]
	// Режим с хешами: ключи спрятаны, хеш остаётся — его называет номером
	// вывод csqtt-cli.
	want := guestErrorLines(fakeCallHash, maskedGuestKeys)
	for i, line := range guestErrorLines(fakeCallHash, fakeGuestKeys) {
		for name, got := range map[string]string{
			"maskLog":   maskLog(line),
			"ParseLine": ParseLine(line).(Log).Text,
		} {
			if got != want[i] {
				t.Errorf("%s(%q) =\n%q\nwant\n%q", name, line, got, want[i])
			}
		}
		if again := maskLog(want[i]); again != want[i] {
			t.Errorf("повторная маскировка меняет строку: %q → %q", want[i], again)
		}
	}
	// auto_js: и ключи, и хеш звонка.
	parse := parseAutoJS(func(s string) Message { return Log{Text: maskLog(s)} })
	wantJS := guestErrorLines("<хеш>", maskedGuestKeys)
	for i, line := range guestErrorLines(fakeCallHash, fakeGuestKeys) {
		got := parse(line).(Log).Text
		if got != wantJS[i] {
			t.Errorf("auto_js %q =\n%q\nwant\n%q", line, got, wantJS[i])
		}
		for _, s := range append(keys, fakeCallHash[:8]) {
			if strings.Contains(got, s) {
				t.Errorf("auto_js: осталось %q: %q", s, got)
			}
		}
	}
	// Ничего лишнего: строки шагов с длинами ключей, client_secret в теле
	// запроса legacy, слово без «?»/«&», пустое значение.
	for _, line := range []string{
		"[STREAM 3] [VKCalls] step1 OK, anonymous_token (120 chars)",
		"[STREAM 3] [VKCalls] step2 OK, user_id=123, secret (32 chars)",
		"[STREAM 3] [VKCalls] step3 OK, OK anonymToken (200 chars)",
		"[STREAM 3] [VKCalls] step4 OK, OK session_key (40 chars)",
		"client_id=1&token_type=messages&client_secret=abc&version=1",
		"secret=abc session_key=def anonymToken=ghi",
		"https://calls.okcdn.ru/fb.do?session_key=&format=JSON&secret=",
		"https://api.vk.me/method/x?my_secret=1&old_session_key=2&anonymous_tokens=3",
	} {
		if got := maskLog(line); got != line {
			t.Errorf("maskLog(%q) = %q", line, got)
		}
	}
	// Ошибка сети «недоступного» хеша в hashes check — тот же адрес шага 2.
	step2, _, _ := guestURLs("fake-hash", fakeGuestKeys)
	masked2, _, _ := guestURLs("fake-hash", maskedGuestKeys)
	line := `HASH_CHECK:{"hash":"fake-hash","message":"error sending request for url (` + step2 + `)","status":"unavailable"}`
	wantHC := HashCheck{Hash: "fake-hash", Status: "unavailable", Message: "error sending request for url (" + masked2 + ")"}
	if got := ParseLine(line); got != wantHC {
		t.Errorf("ParseLine(%q) =\n%#v\nwant\n%#v", line, got, wantHC)
	}
}

// Сторож ключей в адресах: каждый параметр адресов ядра («https://…?…» в
// исходниках) должен быть известен — либо открытый, либо спрятанный
// маскировкой. Новый параметр после обновления ядра роняет тест: решить,
// секрет ли он, и дополнить guestKeyParams или список открытых.
func TestURLParamsInCore(t *testing.T) {
	public := []string{
		"v", "act", "client_id", "device_id", "anonymName", "name", "lang", "extended", "fields",
		"user_id", "session_data", "method", "format", "application_key", "isVideo", "protocolVersion",
		"link", "joinLink", // хеш звонка: maskCallHash или номер в выводе csqtt-cli
		"session_token", // maskSessionToken
		"next",          // тест validated_api_url (vk_js_calls.rs)
	}
	files, err := filepath.Glob(filepath.Join("..", "..", "core", "rust-client", "*.rs"))
	if err != nil || len(files) == 0 {
		t.Fatalf("исходники ядра: %v %v", files, err)
	}
	literal := regexp.MustCompile(`"https://[^"]*\?[^"]*"`)
	param := regexp.MustCompile(`[?&]([A-Za-z_]+)=`)
	seen := map[string]bool{}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, url := range literal.FindAllString(string(src), -1) {
			for _, m := range param.FindAllStringSubmatch(url, -1) {
				seen[m[1]] = true
				if !slices.Contains(public, m[1]) && !slices.Contains(guestKeyParams, m[1]) {
					t.Errorf("%s: неизвестный параметр адреса %q в %s — секрет ли он?", filepath.Base(file), m[1], url)
				}
			}
		}
	}
	for _, p := range guestKeyParams {
		if !seen[p] {
			t.Errorf("параметра %q в адресах ядра нет — сверить guestKeyParams с auth.rs", p)
		}
	}
}

func TestCaptchaError(t *testing.T) {
	inner := errors.New("окно капчи закрыто")
	err := error(&CaptchaError{Reason: "closed", Err: inner})
	if err.Error() != inner.Error() || !errors.Is(err, inner) {
		t.Errorf("CaptchaError = %q", err)
	}
	if got := (&CaptchaError{Reason: "limit"}).Error(); !strings.Contains(got, "limit") {
		t.Errorf("CaptchaError без Err = %q", got)
	}
}

// Сторож выключателя: ядро должно по-прежнему проверять CoreSolverOffEnv в
// начале решателя, до первого запроса к странице капчи. Новая версия ядра без
// этой проверки снова отдавала бы окну испорченную сессию (ERROR_LIMIT), и
// тест должен упасть при обновлении ядра, а не на живой капче.
func TestCoreSolverSwitchInCore(t *testing.T) {
	path := filepath.Join("..", "..", "core", "rust-client", "captcha.rs")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("исходник ядра: %v", err)
	}
	s := string(src)
	fn := strings.Index(s, "async fn solve_once(")
	if fn < 0 {
		t.Fatal("в captcha.rs нет solve_once — решатель ядра устроен иначе, выключатель проверить заново")
	}
	body := s[fn:]
	check := strings.Index(body, `std::env::var_os("`+CoreSolverOffEnv+`")`)
	fetch := strings.Index(body, "fetch_html(")
	if check < 0 || fetch < 0 || check > fetch {
		t.Fatalf("solve_once не проверяет %s до запроса страницы капчи (проверка на %d, запрос на %d)",
			CoreSolverOffEnv, check, fetch)
	}
	if !strings.Contains(body[check:fetch], "bail!") {
		t.Errorf("проверка %s в solve_once не отказывает сразу", CoreSolverOffEnv)
	}
}
