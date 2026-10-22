package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/config"
)

// testLink — выдуманная ссылка панели сервера: адрес из документационной
// сети, пароль и хеши — тестовые значения.
const testLink = "csqtt://connect?v=2&host=203.0.113.20&peer=46001&password=" + "link-password-0123" +
	"&hashes=" + testHashA + "+" + testHashB

func TestProfileAddByFields(t *testing.T) {
	te := newTestEnv(t)
	te.answer(testPassword, testPassword, testHashA+", https://vk.com/call/join/"+testHashB)
	code, stdout, stderr := te.run("profile", "add", "home", "-peer", "203.0.113.10", "-workers", "27", "-datapath", "socks")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "stdout", stdout)
	noSecrets(t, "stderr", stderr)
	if !strings.Contains(stdout, "хешей:  2") {
		t.Errorf("нет числа хешей:\n%s", stdout)
	}
	// Пароль спросили дважды (повтор), хеши — один раз.
	if len(te.asked) != 3 || !strings.Contains(te.asked[1], "Повторите") {
		t.Errorf("вопросы: %q", te.asked)
	}

	file, err := config.Load(te.configPath())
	if err != nil {
		t.Fatalf("файл настроек не читается: %v", err)
	}
	p, err := file.Profile("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Peer != "203.0.113.10" || p.Workers != 27 || p.Datapath != "socks" || p.DeviceID == "" {
		t.Errorf("профиль записан неверно: %+v", p)
	}
	// В файле — только ссылки; в хранилище — разобранные хеши.
	raw, _ := os.ReadFile(te.configPath())
	noSecrets(t, "profiles.toml", string(raw))
	if got, _ := te.store.Get(p.PasswordRef); got != testPassword {
		t.Errorf("пароль в хранилище: %q", got)
	}
	if got, _ := te.store.Get(p.VkHashesRef); got != testHashA+","+testHashB {
		t.Errorf("хеши в хранилище: %q", got)
	}
}

func TestProfileAddFromLink(t *testing.T) {
	te := newTestEnv(t)
	te.answer(testLink)
	code, stdout, stderr := te.run("profile", "add", "work", "-link", "-default")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	for _, text := range []string{stdout, stderr} {
		noSecrets(t, "вывод", text)
		if strings.Contains(text, "link-password") || strings.Contains(text, "csqtt://") {
			t.Errorf("в выводе ссылка или её пароль:\n%s", text)
		}
	}
	if len(te.asked) != 1 || !strings.Contains(te.asked[0], "не отображается") {
		t.Errorf("вопросы: %q", te.asked)
	}

	file, err := config.Load(te.configPath())
	if err != nil {
		t.Fatal(err)
	}
	p := file.Profiles["work"]
	if p == nil || p.Peer != "203.0.113.20:46001" || file.DefaultProfile != "work" {
		t.Fatalf("профиль из ссылки: %+v, default %q", p, file.DefaultProfile)
	}
	if got, _ := te.store.Get(p.PasswordRef); got != "link-password-0123" {
		t.Errorf("пароль из ссылки: %q", got)
	}
	if got, _ := te.store.Get(p.VkHashesRef); got != testHashA+","+testHashB {
		t.Errorf("хеши из ссылки: %q", got)
	}
}

// Ссылка старого вида — без хешей: их спрашивают отдельно.
func TestProfileAddFromLinkWithoutHashes(t *testing.T) {
	te := newTestEnv(t)
	te.answer("csqtt://old-password-1@203.0.113.21:46000", testHashC)
	code, _, stderr := te.run("profile", "add", "old", "-link")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if len(te.asked) != 2 || !strings.Contains(te.asked[1], "Хеши") {
		t.Errorf("вопросы: %q", te.asked)
	}
}

// Негодная ссылка — отказ, и ссылка в сообщении не повторяется.
func TestProfileAddBadLinkNotEchoed(t *testing.T) {
	te := newTestEnv(t)
	bad := "csqtt://connect?v=3&host=203.0.113.20&peer=46001&password=bad-link-secret"
	te.answer(bad)
	code, stdout, stderr := te.run("profile", "add", "work", "-link")
	if code != exitError {
		t.Fatalf("код %d", code)
	}
	if strings.Contains(stdout+stderr, "bad-link-secret") || strings.Contains(stderr, "csqtt://connect") {
		t.Errorf("ссылка в выводе:\n%s%s", stdout, stderr)
	}
	if _, err := os.Stat(te.configPath()); err == nil {
		t.Error("файл настроек записан, хотя ссылка негодная")
	}
}

// Секрет в командной строке — отказ без записи и без повтора значения.
func TestProfileRefusesSecretArgs(t *testing.T) {
	cases := map[string][]string{
		"-password=значение":       {"profile", "set", "home", "-password=" + testPassword},
		"--hashes=значение":        {"profile", "set", "home", "--hashes=" + testHashA},
		"-link=значение":           {"profile", "add", "home", "-link=" + testHashA},
		"значение после -password": {"profile", "set", "home", "-password", testPassword},
		"ссылка аргументом":        {"profile", "add", "home", testLink},
		"ссылка где угодно":        {"connect", testLink},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			te := newTestEnv(t)
			if args[1] == "set" {
				te.addHome()
			}
			before, _ := os.ReadFile(te.configPath())
			code, stdout, stderr := te.run(args...)
			if code != exitUsage {
				t.Errorf("код %d, ожидался %d; stderr:\n%s", code, exitUsage, stderr)
			}
			noSecrets(t, "вывод", stdout+stderr)
			if strings.Contains(stdout+stderr, "link-password") {
				t.Errorf("ссылка в выводе:\n%s", stderr)
			}
			if len(te.asked) > 3 {
				t.Errorf("секрет спросили, хотя команда отклонена: %q", te.asked)
			}
			after, _ := os.ReadFile(te.configPath())
			if string(before) != string(after) {
				t.Error("файл настроек изменился")
			}
		})
	}
}

func TestProfileAddUsage(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"без имени":         {[]string{"profile", "add"}, "не названо имя"},
		"без адреса":        {[]string{"profile", "add", "home"}, "-link"},
		"и ссылка, и адрес": {[]string{"profile", "add", "home", "-link", "-peer", "203.0.113.10"}, "не оба"},
		"имя кириллицей":    {[]string{"profile", "add", "дом", "-peer", "203.0.113.10"}, "латинские"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			te := newTestEnv(t)
			code, _, stderr := te.run(tc.args...)
			if code != exitUsage || !strings.Contains(stderr, tc.want) {
				t.Errorf("код %d, stderr:\n%s", code, stderr)
			}
			if len(te.asked) != 0 {
				t.Errorf("спросили секреты: %q", te.asked)
			}
		})
	}
}

// Флаги проверяются до вопросов о секретах.
func TestProfileAddChecksFlagsBeforeAsking(t *testing.T) {
	te := newTestEnv(t)
	code, _, stderr := te.run("profile", "add", "home", "-peer", "203.0.113.10", "-obfs", "аудио")
	if code != exitError || !strings.Contains(stderr, "obfs") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
	if len(te.asked) != 0 {
		t.Errorf("спросили секреты: %q", te.asked)
	}
}

func TestProfileAddExisting(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	code, _, stderr := te.run("profile", "add", "home", "-peer", "203.0.113.11")
	if code != exitError || !strings.Contains(stderr, "уже есть") || !strings.Contains(stderr, "profile set home") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
}

func TestProfileAddPasswordMismatch(t *testing.T) {
	te := newTestEnv(t)
	te.answer(testPassword, "другой")
	code, _, stderr := te.run("profile", "add", "home", "-peer", "203.0.113.10")
	if code != exitError || !strings.Contains(stderr, "не совпали") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "stderr", stderr)
}

func TestProfileAddTooManyHashes(t *testing.T) {
	te := newTestEnv(t)
	te.answer(testPassword, testPassword, "h1aaaaaa h2aaaaaa h3aaaaaa h4aaaaaa h5aaaaaa h6aaaaaa h7aaaaaa")
	code, _, stderr := te.run("profile", "add", "home", "-peer", "203.0.113.10")
	if code != exitError || !strings.Contains(stderr, "не больше 6") {
		t.Errorf("код %d, stderr:\n%s", code, stderr)
	}
}

// Из конвейера секреты читаются строками stdin, пароль — один раз.
func TestProfileAddFromPipe(t *testing.T) {
	te := newTestEnv(t)
	te.pipe = true
	te.stdin = strings.NewReader(testPassword + "\n" + testHashA + "\n")
	code, stdout, stderr := te.run("profile", "add", "home", "-peer", "203.0.113.10")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "вывод", stdout+stderr)
	file, _ := config.Load(te.configPath())
	if got, _ := te.store.Get(file.Profiles["home"].VkHashesRef); got != testHashA {
		t.Errorf("хеши: %q", got)
	}
}

func TestProfileSet(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	before, _ := config.Load(te.configPath())
	id := before.Profiles["home"].DeviceID

	te.asked = nil
	te.answer(testHashC)
	code, stdout, stderr := te.run("profile", "set", "home", "-workers", "9", "-hashes", "-socks-listen", "127.0.0.1:1081")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "вывод", stdout+stderr)
	if len(te.asked) != 1 {
		t.Errorf("вопросы: %q — пароль спрашивать не просили", te.asked)
	}
	file, _ := config.Load(te.configPath())
	p := file.Profiles["home"]
	if p.Workers != 9 || p.Socks.Listen != "127.0.0.1:1081" || p.Peer != "203.0.113.10" || p.DeviceID != id {
		t.Errorf("профиль после set: %+v", p)
	}
	if got, _ := te.store.Get(p.VkHashesRef); got != testHashC {
		t.Errorf("хеши: %q", got)
	}
}

func TestProfileSetPassword(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	te.answer("новый-пароль", "новый-пароль")
	if code, _, stderr := te.run("profile", "set", "home", "-password"); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	file, _ := config.Load(te.configPath())
	if got, _ := te.store.Get(file.Profiles["home"].PasswordRef); got != "новый-пароль" {
		t.Errorf("пароль: %q", got)
	}
}

func TestProfileSetUsage(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	if code, _, stderr := te.run("profile", "set", "home"); code != exitUsage || !strings.Contains(stderr, "не названо, что менять") {
		t.Errorf("set без флагов: код %d, stderr:\n%s", code, stderr)
	}
	if code, _, stderr := te.run("profile", "set", "work", "-workers", "9"); code != exitError ||
		!strings.Contains(stderr, "в файле есть: home") || !strings.Contains(stderr, "profile add <имя>") {
		t.Errorf("set чужого профиля: код %d, stderr:\n%s", code, stderr)
	}
}

// Значение после булева -password или -hashes встаёт на место имени профиля.
// Такого профиля нет, и сообщение об этом имени не повторяет: это сам секрет.
func TestProfileSetSecretAsNameNotEchoed(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	for _, args := range [][]string{
		{"profile", "set", "-password", testPassword},
		{"profile", "set", "-hashes", testHashA},
		{"profile", "set", "-workers", "9", "-hashes", testHashB},
	} {
		te.asked = nil
		code, stdout, stderr := te.run(args...)
		if code != exitError || !strings.Contains(stderr, "такого профиля нет") || !strings.Contains(stderr, "скрытый ввод") {
			t.Errorf("%q: код %d, stderr:\n%s", args, code, stderr)
		}
		noSecrets(t, "вывод", stdout+stderr)
		if len(te.asked) != 0 {
			t.Errorf("%q: спрошено %q, хотя профиля нет", args, te.asked)
		}
	}
}

// Имя ищется как названо: пустое не означает профиль по умолчанию, а пробелы
// не обрезаются. Иначе set падал на пустом профиле, а rm сообщал об успехе,
// ничего не удалив.
func TestProfileExactName(t *testing.T) {
	te := newTestEnv(t)
	te.addHome("-default")
	before, err := os.ReadFile(te.configPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", " home", "home "} {
		for _, args := range [][]string{
			{"profile", "set", name, "-workers", "9"},
			{"profile", "show", name},
			{"profile", "rm", name, "-y"},
		} {
			code, stdout, stderr := te.run(args...)
			if code != exitError || !strings.Contains(stderr, "такого профиля нет; в файле есть: home") {
				t.Errorf("%q: код %d, stdout:\n%s\nstderr:\n%s", args, code, stdout, stderr)
			}
		}
	}
	after, err := os.ReadFile(te.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("файл настроек изменён:\n%s", after)
	}
	if len(te.store.values) != 2 {
		t.Errorf("секреты тронуты: %d", len(te.store.values))
	}
}

func TestProfileShowHidesSecrets(t *testing.T) {
	te := newTestEnv(t)
	te.addHome("-workers", "126")
	code, stdout, stderr := te.run("profile", "show", "home")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "show", stdout+stderr)
	for _, want := range []string{
		"203.0.113.10:46000",
		"tun", "(умолчание)",
		"password", "задан",
		"vk_hashes", "3 (",
		// 126 воркеров на трёх хешах ядро урежет до 81.
		"запустит 81",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в show нет %q:\n%s", want, stdout)
		}
	}
}

// Секрет не читается (связка заперта) — show говорит об этом, не падая.
func TestProfileShowUnreadableSecret(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	te.store.getErr = config.ErrKeyringLocked
	code, stdout, _ := te.run("profile", "show", "home")
	if code != exitOK || strings.Count(stdout, "не чита") != 2 {
		t.Errorf("код %d:\n%s", code, stdout)
	}
}

func TestProfileList(t *testing.T) {
	te := newTestEnv(t)
	if code, _, stderr := te.run("profile", "list"); code != exitError || !strings.Contains(stderr, "файла настроек нет") {
		t.Errorf("list без файла: код %d, stderr:\n%s", code, stderr)
	}
	te.addHome()
	te.answer(testLink)
	if code, _, stderr := te.run("profile", "add", "work", "-link", "-datapath", "socks", "-default"); code != exitOK {
		t.Fatalf("add work: %s", stderr)
	}
	code, stdout, _ := te.run("profile", "list")
	if code != exitOK {
		t.Fatalf("код %d", code)
	}
	lines := strings.Split(stdout, "\n")
	if !strings.HasPrefix(lines[0], "  home") || !strings.HasPrefix(lines[1], "* work") ||
		!strings.Contains(lines[0], "203.0.113.10:46000") || !strings.Contains(lines[1], "socks") {
		t.Errorf("список:\n%s", stdout)
	}
	noSecrets(t, "list", stdout)
}

func TestProfileRm(t *testing.T) {
	te := newTestEnv(t)
	te.addHome("-default")

	te.stdin = strings.NewReader("n\n")
	if code, stdout, _ := te.run("profile", "rm", "home"); code != exitOK || !strings.Contains(stdout, "Отменено") {
		t.Fatalf("отказ: код %d:\n%s", code, stdout)
	}
	if len(te.store.values) != 2 {
		t.Fatalf("секреты удалены при отказе: %v", te.store.values)
	}

	te.stdin = strings.NewReader("да\n")
	code, stdout, stderr := te.run("profile", "rm", "home")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if len(te.store.values) != 0 {
		t.Errorf("секреты остались: %d", len(te.store.values))
	}
	if !strings.Contains(stdout, "умолчания нет") {
		t.Errorf("про умолчание не сказано:\n%s", stdout)
	}
	file, _ := config.Load(te.configPath())
	if len(file.Profiles) != 0 || file.DefaultProfile != "" {
		t.Errorf("файл после rm: %+v", file)
	}
}

func TestProfileRmWithoutConfirmation(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	if code, _, stderr := te.run("profile", "rm", "home", "-y"); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if code, _, stderr := te.run("profile", "rm", "home", "-y"); code != exitError || !strings.Contains(stderr, "нет") {
		t.Errorf("повторное удаление: код %d, stderr:\n%s", code, stderr)
	}
}

// -kill-switch=false записывает kill_switch = false, -kill-switch — true;
// без флага поле не трогается. show показывает значение, а пропуск — как
// умолчание true.
func TestProfileSetKillSwitch(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	showKillSwitch := func() string {
		t.Helper()
		code, stdout, stderr := te.run("profile", "show", "home")
		if code != exitOK {
			t.Fatalf("show: код %d, stderr:\n%s", code, stderr)
		}
		for _, line := range strings.Split(stdout, "\n") {
			if strings.Contains(line, "kill_switch") {
				return strings.Join(strings.Fields(line), " ")
			}
		}
		t.Fatalf("в show нет kill_switch:\n%s", stdout)
		return ""
	}
	if got := showKillSwitch(); !strings.Contains(got, "true (умолчание)") {
		t.Errorf("show без поля: %q", got)
	}

	for _, tc := range []struct {
		args []string
		want *bool
		show string
	}{
		{[]string{"-kill-switch=false"}, boolPtr(false), "kill_switch false"},
		{[]string{"-workers", "9"}, boolPtr(false), "kill_switch false"},
		{[]string{"-kill-switch"}, boolPtr(true), "kill_switch true"},
	} {
		if code, _, stderr := te.run(append([]string{"profile", "set", "home"}, tc.args...)...); code != exitOK {
			t.Fatalf("%v: код %d, stderr:\n%s", tc.args, code, stderr)
		}
		file, _ := config.Load(te.configPath())
		if got := file.Profiles["home"].KillSwitch; got == nil || *got != *tc.want {
			t.Errorf("%v: kill_switch %v, ждали %v", tc.args, got, *tc.want)
		}
		if got := showKillSwitch(); got != tc.show {
			t.Errorf("%v: show %q, ждали %q", tc.args, got, tc.show)
		}
	}
}

func boolPtr(v bool) *bool { return &v }

// Профиль auto_js хешей не спрашивает и не требует; предупреждение о
// воркерах считает по пределу auto_js, а входа VK нет — подсказка.
func TestProfileAddAutoJS(t *testing.T) {
	te := newTestEnv(t)
	te.answer(testPassword, testPassword)
	code, stdout, stderr := te.run("profile", "add", "js", "-peer", "203.0.113.10", "-vk-hash-mode", "auto_js", "-workers", "126")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if len(te.asked) != 2 {
		t.Errorf("вопросы: %q — хеши в auto_js не нужны", te.asked)
	}
	for _, want := range []string{"vk_hash_mode = auto_js", "в режиме auto_js ядро запустит 72", "vk login"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в выводе нет %q:\n%s", want, stdout)
		}
	}
	file, err := config.Load(te.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if p := file.Profiles["js"]; !p.AutoJS() || p.VkHashesRef != "" || p.Workers != 126 {
		t.Errorf("профиль: %+v", p)
	}
}

// Ссылка с хешами в auto_js: хеши записываются, но в работе не участвуют, и
// об этом сказано.
func TestProfileAddAutoJSFromLink(t *testing.T) {
	te := newTestEnv(t)
	te.answer(testLink)
	code, stdout, stderr := te.run("profile", "add", "js", "-link", "-vk-hash-mode", "auto_js")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "вывод", stdout+stderr)
	if !strings.Contains(stdout, "auto_js не используются") {
		t.Errorf("нет предупреждения о хешах:\n%s", stdout)
	}
	file, _ := config.Load(te.configPath())
	if got, _ := te.store.Get(file.Profiles["js"].VkHashesRef); got != testHashA+","+testHashB {
		t.Errorf("хеши из ссылки: %q", got)
	}
}

// Переключение туда и обратно: хеши профиля остаются на месте; профиль без
// хешей, вернувшийся к manual, спрашивает их сам.
func TestProfileSetVKHashMode(t *testing.T) {
	te := newTestEnv(t)
	te.addHome()
	te.asked = nil
	if code, _, stderr := te.run("profile", "set", "home", "-vk-hash-mode", "auto_js"); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if code, _, stderr := te.run("profile", "set", "home", "-vk-hash-mode", "manual"); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if len(te.asked) != 0 {
		t.Errorf("вопросы: %q — хеши в профиле есть", te.asked)
	}
	file, _ := config.Load(te.configPath())
	if p := file.Profiles["home"]; p.AutoJS() || p.VkHashMode != "manual" || p.VkHashesRef == "" {
		t.Errorf("профиль: %+v", p)
	}

	te.answer(testPassword, testPassword)
	if code, _, stderr := te.run("profile", "add", "js", "-peer", "203.0.113.10", "-vk-hash-mode", "auto_js"); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	te.asked = nil
	te.answer(testHashC)
	if code, _, stderr := te.run("profile", "set", "js", "-vk-hash-mode", ""); code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	if len(te.asked) != 1 || !strings.Contains(te.asked[0], "Хеши") {
		t.Errorf("вопросы: %q", te.asked)
	}
	file, _ = config.Load(te.configPath())
	if got, _ := te.store.Get(file.Profiles["js"].VkHashesRef); got != testHashC {
		t.Errorf("хеши: %q", got)
	}

	if code, _, stderr := te.run("profile", "set", "js", "-vk-hash-mode", "auto_api"); code != exitUsage && code != exitError || !strings.Contains(stderr, `"manual", "auto_js"`) {
		t.Errorf("auto_api: код %d, stderr:\n%s", code, stderr)
	}
}

func TestProfileShowAutoJS(t *testing.T) {
	te := newTestEnv(t)
	te.addHome("-vk-hash-mode", "auto_js", "-workers", "81")
	code, stdout, stderr := te.run("profile", "show", "home")
	if code != exitOK {
		t.Fatalf("код %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "show", stdout+stderr)
	for _, want := range []string{"vk_hash_mode   auto_js", "в режиме auto_js не нужны", "запустит 72"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в show нет %q:\n%s", want, stdout)
		}
	}
}
