package cli

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/config"
	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/supervisor"
)

// Зашифрованные ссылки панели сервера CSQTT 2.5.0 с выдуманными данными —
// те же эталоны, что в internal/config (sealedlink_test.go): сервер
// 203.0.113.10:46000, пароль sealedPassword.
const (
	sealedPassword = "test-password"
	serverToken    = "vk1.a.TestOnlyFakeToken_0123456789-xyz"

	// connect?v=2&host=203.0.113.10&peer=46000&password=test-password&token=<serverToken>
	sealedTokenLink = "csqtt://7SP-1s2C8n2-AjxM_M52KEijaC1JvZtqpJaM7j-xN6WaT1K0DGgMxlgVHoqA-rMSPsmupz1tC5NBM1i16_VBK2w6890M" +
		"zAPgEzihd94BxJm28UzAXtZH8oVFUJHjP2p9hcYSf60ksQX4IbkUzH2-pJEPe_Txh0rUALTPEL8zChsjPPiMuMJ0LA"
	// connect?v=2&host=203.0.113.10&peer=46000&hashes=AbCdEfGhIjKlMnOpQrStUv-1 — без пароля.
	sealedNoPasswordLink = "csqtt://kQNd_JMxPCDhIVpWViay208dNTp8DvwkUc08lGQp6CXPyud9F_CuNidOOGU80ldlq3V2qrSTQcoylFl_8M2EH_yB" +
		"4dwjesq303rng19Oq0sl4r7Xrck7S5_6acZAhxxqOGNHDQ"
	sealedNoPasswordHash = "AbCdEfGhIjKlMnOpQrStUv-1"
)

// noServerToken — ни токена VK от сервера (и его base64 в строке ядра), ни
// пароля из ссылки в тексте.
func noServerToken(t *testing.T, where, text string) {
	t.Helper()
	token64 := base64.StdEncoding.EncodeToString([]byte(`{"token":"` + serverToken + `"}`))
	for _, secret := range []string{serverToken, "TestOnlyFakeToken", token64, sealedPassword,
		sealedTokenLink[8:40], sealedNoPasswordLink[8:40]} {
		if strings.Contains(text, secret) {
			t.Errorf("%s: в выводе секрет %q:\n%s", where, secret, text)
		}
	}
}

// addTokenProfile заводит профиль js из ссылки с токеном VK сервера, режим socks.
func addTokenProfile(t *testing.T, te *testEnv) (stdout, stderr string) {
	t.Helper()
	te.answer(sealedTokenLink)
	code, stdout, stderr := te.run("profile", "add", "js", "-link", "-datapath", "socks", "-socks-listen", freeLoopbackPort(t))
	if code != exitOK {
		t.Fatalf("profile add -link: код %d, stderr:\n%s", code, stderr)
	}
	return stdout, stderr
}

// Ссылка с токеном VK сервера: профиль в auto_js, токен — в хранилище под
// именем профиля, хеши и вход VK не спрашиваются, и сказано, от чьего
// аккаунта звонки. Ни токена, ни пароля в выводе и в файле настроек.
func TestProfileAddFromLinkWithServerToken(t *testing.T) {
	te := newTestEnv(t)
	stdout, stderr := addTokenProfile(t, te)
	noServerToken(t, "вывод", stdout+stderr)
	if len(te.asked) != 1 {
		t.Errorf("вопросы: %q — нужна только ссылка", te.asked)
	}
	for _, want := range []string{"токен VK от сервера записан (", "от аккаунта VK, выданного сервером"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("нет %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Входа VK нет") {
		t.Errorf("просят войти в VK, хотя токен от сервера:\n%s", stdout)
	}

	text, err := os.ReadFile(te.configPath())
	if err != nil {
		t.Fatal(err)
	}
	noServerToken(t, "profiles.toml", string(text))
	p := loadTestFile(t, te).Profiles["js"]
	if p == nil || !p.AutoJS() || p.VkTokenRef != "fake:js.vk_token" || p.VkHashesRef != "" || p.Peer != "203.0.113.10:46000" {
		t.Fatalf("профиль: %+v", p)
	}
	if got, _ := te.store.Get(p.VkTokenRef); got != serverToken {
		t.Errorf("токен в хранилище: %q", got)
	}
	if got, _ := te.store.Get(p.PasswordRef); got != sealedPassword {
		t.Errorf("пароль в хранилище: %q", got)
	}

	code, stdout, stderr := te.run("profile", "show", "js")
	if code != exitOK {
		t.Fatalf("show: код %d, stderr:\n%s", code, stderr)
	}
	noServerToken(t, "show", stdout+stderr)
	// Вид хранилища в скобках свой у каждой системы (на Windows — DPAPI).
	want := "vk_token         задан (" + config.SecretRefKind(p.VkTokenRef) + ")  (выдан сервером: звонки создаются от аккаунта VK владельца сервера"
	if !strings.Contains(stdout, want) {
		t.Errorf("show:\n%s", stdout)
	}
}

// -vk-hash-mode manual главнее токена: хеши спрашиваются, а токен всё равно
// записан — на случай перехода к auto_js.
func TestProfileAddFromLinkWithServerTokenManual(t *testing.T) {
	te := newTestEnv(t)
	te.answer(sealedTokenLink, testHashA)
	code, stdout, stderr := te.run("profile", "add", "js", "-link", "-vk-hash-mode", "manual")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noServerToken(t, "вывод", stdout+stderr)
	p := loadTestFile(t, te).Profiles["js"]
	if p.AutoJS() || !p.ServerVKToken() || p.VkHashesRef == "" {
		t.Errorf("профиль: %+v", p)
	}
	if _, stdout, _ := te.run("profile", "show", "js"); !strings.Contains(stdout, "нужен только в режиме auto_js") {
		t.Errorf("show:\n%s", stdout)
	}
}

// connect берёт токен профиля: без входа VK — без отказа, со входом — всё
// равно токен сервера. vk logout его не трогает, profile rm стирает.
func TestConnectPrefersServerToken(t *testing.T) {
	te := newTestEnv(t)
	addTokenProfile(t, te)
	te.core = "autojs"
	t.Setenv(fakeCoreEnv, te.core)

	var out, errOut syncBuffer
	plan, err := te.env(&out, &errOut).planConnect(connectFlags{configPath: te.configPath(), name: "js"})
	if err != nil {
		t.Fatalf("план без входа VK: %v", err)
	}
	if plan.cfg.VKToken != serverToken || !plan.cfg.AutoJS() || len(plan.cfg.Hashes) != 0 {
		t.Errorf("план без входа VK: токен не тот или не auto_js")
	}

	vkLoggedIn(t, te)
	plan, err = te.env(&out, &errOut).planConnect(connectFlags{configPath: te.configPath(), name: "js"})
	if err != nil {
		t.Fatalf("план со входом VK: %v", err)
	}
	if plan.cfg.VKToken != serverToken {
		t.Error("со входом VK ядру ушёл токен входа, а не токен сервера")
	}

	if code, _, stderr := te.run("vk", "logout"); code != exitOK {
		t.Fatalf("vk logout: код %d, stderr:\n%s", code, stderr)
	}
	if got, err := te.store.Get("fake:js.vk_token"); err != nil || got != serverToken {
		t.Errorf("vk logout стёр токен профиля: %q, %v", got, err)
	}

	if code, _, stderr := te.run("profile", "rm", "js", "-y"); code != exitOK {
		t.Fatalf("profile rm: код %d, stderr:\n%s", code, stderr)
	}
	if len(te.store.values) != 0 {
		t.Errorf("после profile rm в хранилище осталось: %d", len(te.store.values))
	}
}

// Вывод connect называет аккаунт сервера; отказ токена советует новую ссылку,
// а не vk login. Токен в журнале ядра (-v) скрыт.
func TestConnectServerTokenRejected(t *testing.T) {
	te := newTestEnv(t)
	addTokenProfile(t, te)
	te.core = "autojs-rejected"
	code, stdout, stderr := te.run("connect", "-v", "js")
	if code != exitError {
		t.Fatalf("код %d:\n%s\n%s", code, stdout, stderr)
	}
	noServerToken(t, "вывод", stdout+stderr)
	if !strings.Contains(stdout, "звонок VK создаёт ядро (auto_js) от аккаунта VK, выданного сервером") {
		t.Errorf("не сказано, от чьего аккаунта звонки:\n%s", stdout)
	}
	if !strings.Contains(stdout+stderr, "VK_JS_BOOTSTRAP:<скрыт") {
		t.Errorf("строка токена в журнале не видна скрытой:\n%s\n%s", stdout, stderr)
	}
	for _, want := range []string{"токен VK выдан сервером", "csqtt-cli profile set js -link"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("нет %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "vk login") {
		t.Errorf("советуют vk login, хотя токен от сервера:\n%s", stderr)
	}
}

// Токен профиля пропал из хранилища — ошибка до ядра с подсказкой.
func TestConnectServerTokenUnreadable(t *testing.T) {
	te := newTestEnv(t)
	addTokenProfile(t, te)
	te.core = "autojs"
	if err := te.store.Delete("fake:js.vk_token"); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := te.run("connect", "js")
	if code != exitError || !strings.Contains(stderr, "токен VK от сервера (из ссылки csqtt://) профиля js не читается") ||
		!strings.Contains(stderr, "profile set js -link") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
	if strings.Contains(stdout, "профиль js: сервер") {
		t.Errorf("ядро запускалось:\n%s", stdout)
	}
}

// Ссылка без пароля: он спрашивается скрытым вводом, дважды, как при вводе
// вручную; из конвейера — следующей строкой, один раз.
func TestProfileAddFromLinkWithoutPassword(t *testing.T) {
	te := newTestEnv(t)
	te.answer(sealedNoPasswordLink, testPassword, testPassword)
	code, stdout, stderr := te.run("profile", "add", "work", "-link")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "вывод", stdout+stderr)
	if !strings.Contains(stderr, "В ссылке нет пароля") {
		t.Errorf("не сказано, что пароля нет:\n%s", stderr)
	}
	if len(te.asked) != 3 || !strings.Contains(te.asked[1], "Пароль") || !strings.Contains(te.asked[2], "Повторите") {
		t.Errorf("вопросы: %q", te.asked)
	}
	p := loadTestFile(t, te).Profiles["work"]
	if got, _ := te.store.Get(p.PasswordRef); got != testPassword {
		t.Errorf("пароль: %q", got)
	}
	if got, _ := te.store.Get(p.VkHashesRef); got != sealedNoPasswordHash {
		t.Errorf("хеши из ссылки: %q", got)
	}

	te = newTestEnv(t)
	te.pipe = true
	te.stdin = strings.NewReader(sealedNoPasswordLink + "\n" + testPassword + "\n")
	if code, _, stderr := te.run("profile", "add", "work", "-link"); code != exitOK {
		t.Fatalf("из конвейера: код %d, stderr:\n%s", code, stderr)
	}
	p = loadTestFile(t, te).Profiles["work"]
	if got, _ := te.store.Get(p.PasswordRef); got != testPassword {
		t.Errorf("пароль из конвейера: %q", got)
	}
}

// Мастер: ссылка с токеном сервера — ни пароля, ни звонков не спрашивает,
// профиль в auto_js. Замена этого профиля ссылкой с хешами возвращает его к
// хешам и стирает токен прежней ссылки.
func TestWizardServerToken(t *testing.T) {
	tunSupport(t, nil)
	te := newMenuEnv(t)
	te.answer(sealedTokenLink)
	te.input("\nwork\n2\n0\n")
	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	noServerToken(t, "вывод", stdout+stderr)
	if !strings.Contains(stdout, "Ссылка принята: сервер 203.0.113.10:46000, звонки VK создаются от аккаунта, выданного сервером.") {
		t.Errorf("не сказано, что взято из ссылки:\n%s", stdout)
	}
	if len(te.asked) != 1 {
		t.Errorf("скрытых вопросов %d, ожидался один — ссылка: %q", len(te.asked), te.asked)
	}
	p := loadTestFile(t, te).Profiles["work"]
	if !p.AutoJS() || !p.ServerVKToken() {
		t.Fatalf("профиль: %+v", p)
	}

	te.answer(testLink)
	te.input("3\nwork\nда\n2\n0\n")
	if code, _, stderr := te.run("menu"); code != exitOK {
		t.Fatalf("замена: код возврата %d, stderr:\n%s", code, stderr)
	}
	p = loadTestFile(t, te).Profiles["work"]
	if p.AutoJS() || p.ServerVKToken() || p.VkHashesRef == "" {
		t.Errorf("профиль после замены: %+v", p)
	}
	if _, err := te.store.Get("fake:work.vk_token"); err == nil {
		t.Error("токен прежней ссылки остался в хранилище")
	}
}

// Мастер: ссылка без пароля — пароль спрашивается, как при вводе вручную.
func TestWizardLinkWithoutPassword(t *testing.T) {
	tunSupport(t, nil)
	te := newMenuEnv(t)
	te.answer(sealedNoPasswordLink, testPassword, testPassword)
	te.input("\nwork\n2\n0\n")
	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "вывод", stdout+stderr)
	if !strings.Contains(stdout, "Ссылка принята: сервер 203.0.113.10:46000, пароля в ней нет, ссылок на звонки: 1.") {
		t.Errorf("не сказано, что взято из ссылки:\n%s", stdout)
	}
	p := loadTestFile(t, te).Profiles["work"]
	if got, _ := te.store.Get(p.PasswordRef); got != testPassword {
		t.Errorf("пароль: %q", got)
	}
}

// Ошибки конца подключения с токеном сервера советуют новую ссылку.
func TestExplainConnectErrorServerToken(t *testing.T) {
	fatal, ok := coreproc.ParseFatal("[ФАТАЛ] API 5: User authorization failed: invalid access_token (4).")
	if !ok {
		t.Fatal("ParseFatal")
	}
	got := explainConnectError(&supervisor.TokenRejectedError{Fatal: fatal}, "js", true, nil, Build{}).Error()
	if !strings.Contains(got, "токен VK выдан сервером") || !strings.Contains(got, "profile set js -link") ||
		strings.Contains(got, "vk login") {
		t.Errorf("%q", got)
	}
}
