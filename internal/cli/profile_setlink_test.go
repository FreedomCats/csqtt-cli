package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/config"
)

// setLink прогоняет profile set <имя> -link с ответами на скрытый ввод.
func setLink(t *testing.T, te *testEnv, name string, answers []string, extra ...string) (stdout, stderr string) {
	t.Helper()
	te.asked = nil
	te.answer(answers...)
	code, stdout, stderr := te.run(append([]string{"profile", "set", name, "-link"}, extra...)...)
	if code != exitOK {
		t.Fatalf("profile set -link: код %d, stderr:\n%s", code, stderr)
	}
	return stdout, stderr
}

// Новая ссылка заменяет адрес, пароль и хеши; остальные поля и device_id —
// прежние, а прочие флаги применяются вместе с -link.
func TestProfileSetLink(t *testing.T) {
	te := newTestEnv(t)
	te.addHome("-datapath", "socks", "-workers", "30", "-routing-template", "-kill-switch=false")
	before := loadTestFile(t, te).Profiles["home"]

	stdout, stderr := setLink(t, te, "home", []string{testLink}, "-obfs", "video")
	for _, text := range []string{stdout, stderr} {
		noSecrets(t, "вывод", text)
		if strings.Contains(text, "link-password") || strings.Contains(text, "csqtt://") {
			t.Errorf("в выводе ссылка или её пароль:\n%s", text)
		}
	}
	if len(te.asked) != 1 {
		t.Errorf("вопросы: %q — нужна только ссылка", te.asked)
	}
	for _, want := range []string{"изменён", "сервер: 203.0.113.20:46001", "пароль: записан", "хешей:  2"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("нет %q:\n%s", want, stdout)
		}
	}

	p := loadTestFile(t, te).Profiles["home"]
	if p.Peer != "203.0.113.20:46001" || p.DeviceID != before.DeviceID || p.Datapath != "socks" || p.Workers != 30 ||
		!p.RoutingTemplateOn() || p.KillSwitchOn() || p.Obfs != "video" || p.AutoJS() || p.ServerVKToken() {
		t.Errorf("профиль после set -link: %+v", p)
	}
	if got, _ := te.store.Get(p.PasswordRef); got != "link-password-0123" {
		t.Errorf("пароль: %q", got)
	}
	if got, _ := te.store.Get(p.VkHashesRef); got != testHashA+","+testHashB {
		t.Errorf("хеши: %q", got)
	}
	if len(te.store.values) != 2 {
		t.Errorf("записей в хранилище %d, ждали 2 — прежние перезаписаны, а не размножены", len(te.store.values))
	}
}

// Ссылка без хешей: прежние хеши профиля остаются, и об этом сказано.
func TestProfileSetLinkKeepsHashes(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	stdout, stderr := setLink(t, te, "home", []string{"csqtt://new-password-1@203.0.113.21:46000"})
	if len(te.asked) != 1 {
		t.Errorf("вопросы: %q — хеши в профиле есть", te.asked)
	}
	if !strings.Contains(stderr, "В ссылке нет хешей звонков VK — остаются прежние (3).") {
		t.Errorf("не сказано, что хеши прежние:\n%s", stderr)
	}
	if strings.Contains(stdout, "хешей:") {
		t.Errorf("хеши будто бы записаны заново:\n%s", stdout)
	}
	p := loadTestFile(t, te).Profiles["home"]
	if p.Peer != "203.0.113.21:46000" {
		t.Errorf("адрес: %q", p.Peer)
	}
	if got, _ := te.store.Get(p.PasswordRef); got != "new-password-1" {
		t.Errorf("пароль: %q", got)
	}
	if got, _ := te.store.Get(p.VkHashesRef); got != testHashA+","+testHashB+","+testHashC {
		t.Errorf("хеши: %q", got)
	}
}

// Ссылка с токеном VK сервера переводит профиль в auto_js; следующая ссылка
// без токена токен стирает и возвращает профиль к manual с хешами из ссылки.
func TestProfileSetLinkServerToken(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	stdout, stderr := setLink(t, te, "home", []string{sealedTokenLink})
	noServerToken(t, "вывод", stdout+stderr)
	if len(te.asked) != 1 {
		t.Errorf("вопросы: %q — нужна только ссылка", te.asked)
	}
	if !strings.Contains(stdout, "токен VK от сервера записан (") || !strings.Contains(stdout, "от аккаунта VK, выданного сервером") {
		t.Errorf("не сказано о токене сервера:\n%s", stdout)
	}
	p := loadTestFile(t, te).Profiles["home"]
	if !p.AutoJS() || !p.ServerVKToken() || p.Peer != "203.0.113.10:46000" || p.VkHashesRef == "" {
		t.Fatalf("профиль с токеном: %+v", p)
	}
	if got, _ := te.store.Get(p.VkTokenRef); got != serverToken {
		t.Errorf("токен в хранилище: %q", got)
	}

	setLink(t, te, "home", []string{testLink})
	p = loadTestFile(t, te).Profiles["home"]
	if p.AutoJS() || p.ServerVKToken() || p.Peer != "203.0.113.20:46001" {
		t.Errorf("профиль после ссылки без токена: %+v", p)
	}
	if _, err := te.store.Get("fake:home.vk_token"); err == nil {
		t.Error("токен прежней ссылки остался в хранилище")
	}
	if got, _ := te.store.Get(p.VkHashesRef); got != testHashA+","+testHashB {
		t.Errorf("хеши: %q", got)
	}
}

// Профиль из ссылки с токеном хешей не имеет: вернувшись к manual по ссылке
// без хешей и без токена, он спрашивает хеши.
func TestProfileSetLinkFromTokenAsksHashes(t *testing.T) {
	te := newTestEnv(t)
	addTokenProfile(t, te)
	_, stderr := setLink(t, te, "js", []string{"csqtt://new-password-1@203.0.113.21:46000", testHashC})
	if len(te.asked) != 2 || !strings.Contains(te.asked[1], "Хеши") {
		t.Errorf("вопросы: %q", te.asked)
	}
	if !strings.Contains(stderr, "В ссылке нет хешей звонков VK.") {
		t.Errorf("не сказано, что хешей нет:\n%s", stderr)
	}
	p := loadTestFile(t, te).Profiles["js"]
	if p.AutoJS() || p.ServerVKToken() {
		t.Errorf("профиль: %+v", p)
	}
	if got, _ := te.store.Get(p.VkHashesRef); got != testHashC {
		t.Errorf("хеши: %q", got)
	}
}

// Названный -vk-hash-mode главнее токена: режим manual, а токен записан — на
// случай перехода к auto_js. И профиль auto_js по своему входу VK ссылка без
// токена из auto_js не выводит.
func TestProfileSetLinkExplicitMode(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	setLink(t, te, "home", []string{sealedTokenLink}, "-vk-hash-mode", "manual")
	p := loadTestFile(t, te).Profiles["home"]
	if p.AutoJS() || p.VkHashMode != config.VKHashModeManual || !p.ServerVKToken() {
		t.Errorf("профиль: %+v", p)
	}
	if got, _ := te.store.Get(p.VkTokenRef); got != serverToken {
		t.Errorf("токен в хранилище: %q", got)
	}

	te = newTestEnv(t)
	te.addHome("-vk-hash-mode", "auto_js")
	te.answers = nil // хешей профиль auto_js не спрашивал
	setLink(t, te, "home", []string{testLink})
	if p := loadTestFile(t, te).Profiles["home"]; !p.AutoJS() || p.ServerVKToken() {
		t.Errorf("профиль auto_js по входу VK: %+v", p)
	}
}

// Ссылка без пароля: он спрашивается дважды, а из конвейера — следующей
// строкой, один раз.
func TestProfileSetLinkWithoutPassword(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	_, stderr := setLink(t, te, "home", []string{sealedNoPasswordLink, "новый-пароль", "новый-пароль"})
	if !strings.Contains(stderr, "В ссылке нет пароля подключения.") {
		t.Errorf("не сказано, что пароля нет:\n%s", stderr)
	}
	if len(te.asked) != 3 || !strings.Contains(te.asked[1], "пароль") || !strings.Contains(te.asked[2], "Повторите") {
		t.Errorf("вопросы: %q", te.asked)
	}
	p := loadTestFile(t, te).Profiles["home"]
	if got, _ := te.store.Get(p.PasswordRef); got != "новый-пароль" {
		t.Errorf("пароль: %q", got)
	}
	if got, _ := te.store.Get(p.VkHashesRef); got != sealedNoPasswordHash {
		t.Errorf("хеши из ссылки: %q", got)
	}

	te = newTestEnv(t)
	te.addHome()
	te.pipe = true
	te.stdin = strings.NewReader(sealedNoPasswordLink + "\n" + "пароль-из-конвейера" + "\n")
	if code, _, stderr := te.run("profile", "set", "home", "-link"); code != exitOK {
		t.Fatalf("из конвейера: код %d, stderr:\n%s", code, stderr)
	}
	p = loadTestFile(t, te).Profiles["home"]
	if got, _ := te.store.Get(p.PasswordRef); got != "пароль-из-конвейера" {
		t.Errorf("пароль из конвейера: %q", got)
	}
}

// -link с -peer, -password или -hashes — ошибка использования до вопросов.
func TestProfileSetLinkUsage(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"-peer":     {[]string{"-peer", "203.0.113.11"}, "не оба сразу"},
		"-password": {[]string{"-password"}, "-password с -link не нужен"},
		"-hashes":   {[]string{"-hashes"}, "-hashes с -link не ставится"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			te := newTestEnv(t)
			te.addHome()
			te.asked = nil
			before, _ := os.ReadFile(te.configPath())
			code, _, stderr := te.run(append([]string{"profile", "set", "home", "-link"}, tc.args...)...)
			if code != exitUsage || !strings.Contains(stderr, tc.want) {
				t.Errorf("код %d, stderr:\n%s", code, stderr)
			}
			if len(te.asked) != 0 {
				t.Errorf("спросили: %q", te.asked)
			}
			if after, _ := os.ReadFile(te.configPath()); string(after) != string(before) {
				t.Error("файл настроек изменился")
			}
		})
	}
}

// Метка порядка байт в начале строки из конвейера снимается: и у пароля, и у
// ссылки. Внутри строки она не трогается.
func TestPipeStripsByteOrderMark(t *testing.T) {
	te := newTestEnv(t)
	te.pipe = true
	te.stdin = strings.NewReader(byteOrderMark + testPassword + "\n" + testHashA + "\n")
	if code, _, stderr := te.run("profile", "add", "home", "-peer", "203.0.113.10"); code != exitOK {
		t.Fatalf("add: код %d, stderr:\n%s", code, stderr)
	}
	p := loadTestFile(t, te).Profiles["home"]
	if got, _ := te.store.Get(p.PasswordRef); got != testPassword {
		t.Errorf("пароль: %q", got)
	}

	te.stdin = strings.NewReader(byteOrderMark + testLink + "\n")
	if code, _, stderr := te.run("profile", "set", "home", "-link"); code != exitOK {
		t.Fatalf("set -link: код %d, stderr:\n%s", code, stderr)
	}
	p = loadTestFile(t, te).Profiles["home"]
	if got, _ := te.store.Get(p.PasswordRef); p.Peer != "203.0.113.20:46001" || got != "link-password-0123" {
		t.Errorf("ссылка с BOM: адрес %q, пароль %q", p.Peer, got)
	}

	inner := "пароль" + byteOrderMark + "внутри"
	te.stdin = strings.NewReader(inner + "\n")
	if code, _, stderr := te.run("profile", "set", "home", "-password"); code != exitOK {
		t.Fatalf("set -password: код %d, stderr:\n%s", code, stderr)
	}
	if got, _ := te.store.Get(p.PasswordRef); got != inner {
		t.Errorf("метка внутри строки тронута: %q", got)
	}
}
