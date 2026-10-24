package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/config"
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
	"github.com/FreedomCats/csqtt-cli/internal/supervisor"
)

// newHash — выдуманный хеш нового звонка, который вводят в меню.
const newHash = "newhashDDDDDDDDDDDDDDDDDDDDDDDDD"

// newMenuEnv — testEnv, который притворяется терминалом.
func newMenuEnv(t *testing.T) *testEnv {
	t.Helper()
	te := newTestEnv(t)
	te.tty = true
	return te
}

// input задаёт строки stdin: ответы на вопросы меню, не скрытые.
func (te *testEnv) input(lines string) { te.stdin = strings.NewReader(lines) }

// tunSupport подменяет признак «tun на этой системе работает»: nil —
// работает (Linux), иначе — причина, по которой нет (как на Windows).
func tunSupport(t *testing.T, unsupported error) {
	t.Helper()
	withTunGuard(t, nil)
	prev := newTun
	t.Cleanup(func() { newTun = prev })
	newTun = func(datapath.TunOptions) tunDatapath {
		return &fakeConnectTun{j: &tunJournal{}, unsupported: unsupported}
	}
}

// runAsRoot подменяет проверку прав root.
func runAsRoot(t *testing.T, root bool) {
	t.Helper()
	prev := isRoot
	t.Cleanup(func() { isRoot = prev })
	isRoot = func() bool { return root }
}

// storedHashes — хеши профиля из подставного хранилища.
func storedHashes(t *testing.T, te *testEnv, name string) []string {
	t.Helper()
	p := loadTestFile(t, te).Profiles[name]
	if p == nil {
		t.Fatalf("профиля %s нет", name)
	}
	raw, err := te.store.Get(p.VkHashesRef)
	if err != nil {
		t.Fatal(err)
	}
	return config.ParseHashes(raw)
}

// В терминале csqtt-cli без аргументов открывает меню, а не список команд.
func TestNoArgsOpensMenuInTerminal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)

	var out, errOut bytes.Buffer
	e := &env{
		stdout: &out,
		stderr: &errOut,
		stdin:  strings.NewReader(""),
		tty:    func() bool { return true },
	}
	code := run(context.Background(), e, nil)

	if code != exitOK {
		t.Errorf("код возврата = %d, ожидался %d; stderr:\n%s", code, exitOK, errOut.String())
	}
	if !strings.Contains(out.String(), "Профилей пока нет") {
		t.Errorf("меню не открылось:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Использование") {
		t.Errorf("вместо меню — список команд:\n%s", out.String())
	}
	if !e.noPause {
		t.Error("уход из меню концом ввода не отменил паузу перед закрытием окна")
	}
	if !wantsMenu(e, nil) || !wantsMenu(e, []string{"menu"}) || wantsMenu(e, []string{"menu", "-h"}) ||
		wantsMenu(e, []string{"connect"}) {
		t.Error("wantsMenu ошибается: от него зависит, кто разбирает сигналы")
	}
}

// Без терминала команда menu отказывает: отвечать на её вопросы некому.
func TestMenuNeedsTerminal(t *testing.T) {
	te := newTestEnv(t)
	code, _, stderr := te.run("menu")
	if code != exitError {
		t.Errorf("код возврата = %d, ожидался %d", code, exitError)
	}
	if !strings.Contains(stderr, "только в терминале") {
		t.Errorf("непонятное сообщение:\n%s", stderr)
	}
	if wantsMenu(te.env(&bytes.Buffer{}, &bytes.Buffer{}), nil) {
		t.Error("без терминала запуск без аргументов открыл бы меню")
	}
}

func TestMenuListsProfiles(t *testing.T) {
	te := newMenuEnv(t)
	te.addHome("-datapath", "socks")
	te.input("0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"> home", "203.0.113.10:46000", "только программы с прокси", "(по умолчанию)",
		"1. Подключиться (home)", "6. Удалить профиль home", "0. Выход", "До свидания"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в меню нет %q:\n%s", want, stdout)
		}
	}
	noSecrets(t, "stdout", stdout)
}

// Неверный ответ переспрашивается, а не закрывает меню.
func TestMenuRepeatsQuestionOnBadChoice(t *testing.T) {
	te := newMenuEnv(t)
	te.addHome()
	te.input("9\nабв\n0\n")

	code, _, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if got := strings.Count(stderr, "Нет такого пункта"); got != 2 {
		t.Errorf("переспросили %d раз, ожидалось 2:\n%s", got, stderr)
	}
	if got := strings.Count(stderr, "Выберите пункт"); got != 3 {
		t.Errorf("вопрос задан %d раз, ожидалось 3:\n%s", got, stderr)
	}
}

// Подключение, которое не удалось, возвращает в меню: человек видит ошибку
// и может исправить профиль, не перезапуская csqtt-cli.
func TestMenuConnectErrorReturnsToMenu(t *testing.T) {
	te := newMenuEnv(t)
	socksHome(t, te)
	// Секреты не читаются — план подключения не соберётся, до сети дело не
	// дойдёт.
	te.store.getErr = errors.New("связка ключей не разблокирована")
	te.input("\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "Не получилось: пароль профиля home: связка ключей не разблокирована") {
		t.Errorf("ошибка подключения не показана:\n%s", stderr)
	}
	if got := strings.Count(stdout, "1. Подключиться"); got != 2 {
		t.Errorf("меню показано %d раз, ожидалось 2 — до и после подключения:\n%s", got, stdout)
	}
}

// Выбор другого профиля меняет, к чему подключается первый пункт, но не
// трогает default_profile.
func TestMenuPicksAnotherProfile(t *testing.T) {
	te := newMenuEnv(t)
	te.addHome("-default")
	te.answer(testPassword, testPassword, testHashA)
	if code, _, stderr := te.run("profile", "add", "second", "-peer", "198.51.100.7"); code != exitOK {
		t.Fatalf("profile add: код %d, stderr:\n%s", code, stderr)
	}
	te.input("2\n2\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "1. Подключиться (second)") {
		t.Errorf("выбор профиля не подействовал:\n%s", stdout)
	}
	if file := loadTestFile(t, te); file.DefaultProfile != "home" {
		t.Errorf("default_profile = %q, ожидался home", file.DefaultProfile)
	}
}

// Удаление — через profile rm: с подтверждением и секретами.
func TestMenuRemovesProfile(t *testing.T) {
	te := newMenuEnv(t)
	te.addHome()
	te.input("6\nда\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "удалён") || !strings.Contains(stdout, "Профилей пока нет") {
		t.Errorf("профиль не удалён:\n%s", stdout)
	}
	if len(te.store.values) != 0 {
		t.Errorf("секреты остались: %d", len(te.store.values))
	}
}

func TestMenuNumber(t *testing.T) {
	for _, tc := range []struct {
		line string
		want int
		ok   bool
	}{
		{"", 1, true},
		{" 3 ", 3, true},
		{"0", 0, true},
		{"7", 0, false},
		{"-1", 0, false},
		{"один", 0, false},
	} {
		got, ok := menuNumber(tc.line, 6, 1)
		if got != tc.want || ok != tc.ok {
			t.Errorf("menuNumber(%q) = %d, %v; ожидалось %d, %v", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}

// ──────────────────────────── мастер ────────────────────────────

// Мастер с нуля: Enter на ссылке и на имени, адрес без порта, режим по
// умолчанию — весь трафик. Первый профиль становится профилем по умолчанию.
func TestWizardCreatesFirstProfile(t *testing.T) {
	tunSupport(t, nil)
	te := newMenuEnv(t)
	te.answer("", testPassword, testPassword, "https://vk.com/call/join/"+testHashA+" "+testHashC)
	te.input("\n\nexample.org\n\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"Порт не указан — взят 46000: example.org:46000", "Принято ссылок: 2", "создан",
		"1. Весь трафик компьютера", "1. Подключиться (home)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в выводе нет %q:\n%s", want, stdout)
		}
	}
	noSecrets(t, "stdout", stdout)
	noSecrets(t, "stderr", stderr)
	// Ссылка, пароль дважды и ссылки на звонки — всё скрытым вводом.
	if len(te.asked) != 4 {
		t.Errorf("скрытых вопросов %d: %q", len(te.asked), te.asked)
	}

	file := loadTestFile(t, te)
	h := file.Profiles["home"]
	if h == nil || h.Peer != "example.org:46000" || h.Datapath != config.DatapathTun || h.DeviceID == "" {
		t.Fatalf("профиль записан неверно: %+v", h)
	}
	if file.DefaultProfile != "home" {
		t.Errorf("default_profile = %q, ожидался home", file.DefaultProfile)
	}
	if got, _ := te.store.Get(h.PasswordRef); got != testPassword {
		t.Errorf("пароль записан неверно: %q", got)
	}
	if got := strings.Join(storedHashes(t, te, "home"), ","); got != testHashA+","+testHashC {
		t.Errorf("хеши записаны неверно: %q", got)
	}
}

// Ссылка csqtt:// с хешами: адрес, пароль и звонки — из неё, спрашиваются
// только имя и режим. Ни ссылки, ни пароля из неё в выводе нет.
func TestWizardFromShareLink(t *testing.T) {
	tunSupport(t, nil)
	te := newMenuEnv(t)
	te.answer(testLink)
	te.input("\nwork\n2\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	for _, text := range []string{stdout, stderr} {
		noSecrets(t, "вывод", text)
		if strings.Contains(text, "link-password") || strings.Contains(text, "csqtt://connect") {
			t.Errorf("в выводе ссылка или её пароль:\n%s", text)
		}
	}
	if !strings.Contains(stdout, "Ссылка принята: сервер 203.0.113.20:46001, ссылок на звонки: 2.") {
		t.Errorf("не сказано, что взято из ссылки:\n%s", stdout)
	}
	if len(te.asked) != 1 {
		t.Errorf("скрытых вопросов %d, ожидался один — ссылка: %q", len(te.asked), te.asked)
	}

	file := loadTestFile(t, te)
	p := file.Profiles["work"]
	if p == nil || p.Peer != "203.0.113.20:46001" || p.Datapath != config.DatapathSocks || file.DefaultProfile != "work" {
		t.Fatalf("профиль из ссылки: %+v, default %q", p, file.DefaultProfile)
	}
	if got, _ := te.store.Get(p.PasswordRef); got != "link-password-0123" {
		t.Errorf("пароль из ссылки: %q", got)
	}
	if got := strings.Join(storedHashes(t, te, "work"), ","); got != testHashA+","+testHashB {
		t.Errorf("хеши из ссылки: %q", got)
	}
}

// Ссылка старого вида без хешей: ссылки на звонки спрашиваются отдельно.
func TestWizardShareLinkWithoutHashes(t *testing.T) {
	tunSupport(t, nil)
	te := newMenuEnv(t)
	te.answer("csqtt://old-password-1@203.0.113.21:46000", testHashC)
	te.input("\n\n\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "ссылок на звонки в ней нет") {
		t.Errorf("не сказано, что звонков в ссылке нет:\n%s", stdout)
	}
	if len(te.asked) != 2 || !strings.Contains(te.asked[1], "Ссылки на звонки") {
		t.Errorf("вопросы: %q", te.asked)
	}
	if got := strings.Join(storedHashes(t, te, "home"), ","); got != testHashC {
		t.Errorf("хеши: %q", got)
	}
}

// Второй профиль: имя занято — от замены отказались, и предлагается
// свободное; режим SOCKS5; прежний единственный профиль остаётся профилем по
// умолчанию, а меню переходит к новому.
func TestWizardCreatesSecondProfile(t *testing.T) {
	tunSupport(t, nil)
	te := newMenuEnv(t)
	te.addHome()
	te.answer("", testPassword, testPassword, testHashC)
	te.input("3\nhome\n\n\n198.51.100.7:46000\n2\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "Профиль home уже есть") || !strings.Contains(stderr, "Назовите новый профиль иначе") {
		t.Errorf("занятое имя не отвергнуто:\n%s", stderr)
	}
	if !strings.Contains(stdout, "1. Подключиться (profile-2)") {
		t.Errorf("меню не перешло к новому профилю:\n%s", stdout)
	}

	file := loadTestFile(t, te)
	if got := file.Profiles["profile-2"]; got == nil || got.Datapath != config.DatapathSocks {
		t.Errorf("второй профиль записан неверно: %+v", got)
	}
	if file.DefaultProfile != "home" {
		t.Errorf("default_profile = %q, ожидался home", file.DefaultProfile)
	}
}

// Замена профиля мастером сохраняет device_id: сервер привязывает пароль к
// устройству.
func TestWizardReplaceKeepsDeviceID(t *testing.T) {
	tunSupport(t, nil)
	te := newMenuEnv(t)
	te.addHome()
	id := loadTestFile(t, te).Profiles["home"].DeviceID
	te.answer(testLink)
	te.input("3\nhome\nда\n\n0\n")

	if code, _, stderr := te.run("menu"); code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	p := loadTestFile(t, te).Profiles["home"]
	if p.DeviceID != id || p.Peer != "203.0.113.20:46001" {
		t.Errorf("профиль после замены: %+v, прежний device_id %s", p, id)
	}
}

// Ошибки в ответах переспрашиваются: негодная ссылка, имя и адрес,
// несовпавший и пустой пароль, пустой ответ и ввод без единой ссылки и с
// лишними, неверный режим. Пустой ответ в мастере — не отмена, в отличие от
// пунктов правки (menu_edit_test.go).
func TestWizardRepeatsBadAnswers(t *testing.T) {
	tunSupport(t, nil)
	te := newMenuEnv(t)
	var tooMany []string
	for i := range config.MaxHashes + 1 {
		tooMany = append(tooMany, fmt.Sprintf("manyhash%024d", i))
	}
	te.answer("csqtt://connect?v=3&host=203.0.113.20&peer=46001&password=bad-link-secret", "",
		"", testPassword, "другой", testPassword, testPassword,
		"", "https://example.org/", strings.Join(tooMany, " "), testHashA)
	te.input("\nмой дом\n\n\nexample.org:порт\n203.0.113.5:443\n5\n1\n0\n")

	code, _, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"Ссылка не подошла", "Такое имя не подойдёт", "Адрес сервера нужен", "Не похоже на адрес",
		"Пароль не может быть пустым", "Пароли не совпали", "Не нашлось ни одной ссылки", "Ссылок 7, а поместится 6",
		"Введите 1 или 2"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("нет переспроса %q:\n%s", want, stderr)
		}
	}
	if n := strings.Count(stderr, "Не нашлось ни одной ссылки"); n != 2 {
		t.Errorf("ответ без ссылок переспрошен %d раз, ожидалось 2 (пустой и без ссылок):\n%s", n, stderr)
	}
	if strings.Contains(stderr, "bad-link-secret") {
		t.Errorf("негодная ссылка повторена:\n%s", stderr)
	}
	if len(te.answers) != 0 {
		t.Errorf("не все ответы на скрытый ввод спрошены: осталось %d", len(te.answers))
	}
	if got := loadTestFile(t, te).Profiles["home"]; got == nil || got.Peer != "203.0.113.5:443" {
		t.Errorf("профиль записан неверно: %+v", got)
	}
}

// Конец ввода на любом вопросе мастера — отмена: профиль не записывается.
// Секреты здесь читаются строками stdin, как из конвейера: так конец ввода
// приходит в одном потоке на любом вопросе.
func TestWizardCancelledOnEachQuestion(t *testing.T) {
	for _, c := range []struct {
		name  string
		input string
		home  bool
	}{
		{"ссылка", "\n", false},
		{"имя", "\n\n", false},
		{"адрес", "\n\n\n", false},
		{"пароль", "\n\n\nexample.org\n", false},
		{"повтор пароля", "\n\n\nexample.org\nпароль\n", false},
		{"ссылки на звонки", "\n\n\nexample.org\nпароль\nпароль\n", false},
		{"режим", "\n\n\nexample.org\nпароль\nпароль\n" + testHashA + "\n", false},
		{"режим после ссылки", "\n" + testLink + "\n\n", false},
		{"замена занятого имени", "3\n\nhome\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			tunSupport(t, nil)
			te := newMenuEnv(t)
			if c.home {
				te.addHome()
			}
			before, _ := os.ReadFile(te.configPath())
			te.pipe = true
			te.input(c.input)

			code, stdout, stderr := te.run("menu")
			if code != exitOK {
				t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
			}
			if !strings.Contains(stdout, "Профиль не записан") {
				t.Errorf("не сказано, что профиль не записан:\n%s", stdout)
			}
			after, err := os.ReadFile(te.configPath())
			if c.home {
				if string(after) != string(before) {
					t.Error("файл настроек изменён при отмене")
				}
			} else if !os.IsNotExist(err) {
				t.Error("файл настроек создан при отмене")
			}
			if n := len(te.store.values); c.home && n != 2 || !c.home && n != 0 {
				t.Errorf("в хранилище %d секретов", n)
			}
		})
	}
}

// Где tun работает только без защиты DNS и kill-switch (Windows до W5),
// мастер его не предлагает: согласие на утечку — -allow-leak командной
// строки. Причина и команда — в строке о режиме.
func TestWizardModeWithoutGuard(t *testing.T) {
	tunSupport(t, nil)
	withTunGuard(t, errors.New("на Windows он пока работает без защиты от утечки DNS и kill-switch"))
	te := newMenuEnv(t)
	te.answer("", testPassword, testPassword, testHashA)
	te.input("\n\nexample.org\n\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "здесь недоступен: на Windows он пока работает без защиты от утечки DNS и kill-switch "+
		"(подключиться так можно только из командной строки: csqtt-cli connect -datapath tun -allow-leak)") ||
		strings.Contains(stdout, "Весь трафик компьютера.") {
		t.Errorf("режимы показаны не так:\n%s", stdout)
	}
	if got := loadTestFile(t, te).Profiles["home"]; got == nil || got.Datapath != config.DatapathSocks {
		t.Errorf("профиль: %+v", got)
	}
}

// Где tun не работает (Windows до своего адаптера), мастер по Enter выбирает
// socks и говорит, почему весь трафик недоступен; tun не предлагается.
func TestWizardModeWithoutTun(t *testing.T) {
	tunSupport(t, errors.New("datapath tun пока работает только на Linux"))
	te := newMenuEnv(t)
	te.answer("", testPassword, testPassword, testHashA)
	te.input("\n\nexample.org\n2\n\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "1. Только программы, в которых указан прокси SOCKS5 127.0.0.1:1080") ||
		!strings.Contains(stdout, "здесь недоступен: datapath tun пока работает только на Linux") {
		t.Errorf("режимы без tun показаны не так:\n%s", stdout)
	}
	if strings.Contains(stdout, "Весь трафик компьютера.") {
		t.Errorf("предложен tun, которого нет:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Введите 1.") {
		t.Errorf("пункт 2 не отвергнут:\n%s", stderr)
	}
	if got := loadTestFile(t, te).Profiles["home"]; got == nil || got.Datapath != config.DatapathSocks {
		t.Errorf("профиль записан неверно: %+v", got)
	}
}

// ──────────────────────────── подключение ────────────────────────────

// Звонки закрылись посреди сеанса — меню предлагает новые ссылки и
// подключается снова уже с ними.
func TestMenuConnectAsksNewHashesWhenExhausted(t *testing.T) {
	te := newMenuEnv(t)
	socksHome(t, te)
	var runs [][]string
	// План ищет ядро, но подъём подставной: ядро не запускается.
	te.core = "connected"
	te.connectRun = func(_ context.Context, plan *connectPlan) error {
		runs = append(runs, plan.cfg.Hashes)
		if len(runs) == 1 {
			return &supervisor.HashesExhaustedError{Dead: []supervisor.DeadHash{{Code: 954}}}
		}
		return nil
	}
	te.answer("https://vk.com/call/join/" + newHash)
	te.input("\nда\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "больше не работает") {
		t.Errorf("не сказано, что звонки закрыты:\n%s", stderr)
	}
	if len(runs) != 2 || strings.Join(runs[1], " ") != newHash {
		t.Errorf("подключения: %q", runs)
	}
	if strings.Contains(stdout+stderr, newHash) {
		t.Errorf("новый хеш в выводе:\n%s%s", stdout, stderr)
	}
}

// То же с поддельным ядром через настоящие supervisor и plan.run: все
// звонки недоступны — новые ссылки записываются; недоступны и они — снова
// вопрос, отказ — меню.
func TestMenuConnectExhaustedRealCore(t *testing.T) {
	te := newMenuEnv(t)
	socksHome(t, te)
	te.core = "dead"
	te.answer(newHash)
	te.input("\nда\n\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	noSecrets(t, "вывод", stdout+stderr)
	if strings.Contains(stdout+stderr, newHash) {
		t.Errorf("новый хеш в выводе:\n%s%s", stdout, stderr)
	}
	if got := strings.Count(stderr, "больше не работает"); got != 2 {
		t.Errorf("вопрос о новых ссылках задан %d раз, ожидалось 2:\n%s", got, stderr)
	}
	if got := strings.Join(storedHashes(t, te, "home"), ","); got != newHash {
		t.Errorf("хеши после замены: %q", got)
	}
	if got := strings.Count(stdout, "1. Подключиться"); got != 2 {
		t.Errorf("меню показано %d раз, ожидалось 2:\n%s", got, stdout)
	}
}

// Отказ вводить новые ссылки — снова меню, профиль не тронут.
func TestMenuConnectExhaustedDeclined(t *testing.T) {
	te := newMenuEnv(t)
	socksHome(t, te)
	runs := 0
	// План ищет ядро, но подъём подставной: ядро не запускается.
	te.core = "connected"
	te.connectRun = func(context.Context, *connectPlan) error {
		runs++
		return fmt.Errorf("конец: %w", &supervisor.HashesExhaustedError{})
	}
	te.input("\n\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if runs != 1 {
		t.Errorf("подключений %d, ожидалось 1", runs)
	}
	if n := strings.Count(stdout, "1. Подключиться"); n != 2 {
		t.Errorf("меню показано %d раз, ожидалось 2:\n%s", n, stdout)
	}
	if got := len(storedHashes(t, te, "home")); got != 3 {
		t.Errorf("хешей %d, ожидалось 3", got)
	}
}

// Профиль в режиме tun, прав root нет, и взять их неоткуда: причина и
// предложение подключиться в socks на этот раз. Профиль не меняется.
func TestMenuTunNoElevationOffersSocks(t *testing.T) {
	for _, agree := range []bool{true, false} {
		t.Run(fmt.Sprintf("согласие %v", agree), func(t *testing.T) {
			tunSupport(t, nil)
			runAsRoot(t, false)
			te := newMenuEnv(t)
			te.addHome()
			relaunches := 0
			te.elevate = func([]string) (bool, error) {
				relaunches++
				return false, &noElevationError{reason: "у этой учётной записи нет прав"}
			}
			var modes []string
			// План ищет ядро, но подъём подставной: ядро не запускается.
			te.core = "connected"
			te.connectRun = func(_ context.Context, plan *connectPlan) error {
				modes = append(modes, plan.mode)
				return nil
			}
			if agree {
				te.input("\nда\n0\n")
			} else {
				te.input("\n\n0\n")
			}

			code, _, stderr := te.run("menu")
			if code != exitOK {
				t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
			}
			for _, want := range []string{"У этой учётной записи нет прав.",
				"Подключиться на этот раз в режиме socks"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("нет %q:\n%s", want, stderr)
				}
			}
			if strings.Contains(stderr, "sudo csqtt-cli connect") {
				t.Errorf("подсказка командной строки в меню:\n%s", stderr)
			}
			if relaunches != 1 {
				t.Errorf("перезапусков %d, ожидался 1", relaunches)
			}
			if agree && (len(modes) != 1 || modes[0] != config.DatapathSocks) {
				t.Errorf("подключения: %q, ожидалось одно в socks", modes)
			}
			if !agree && len(modes) != 0 {
				t.Errorf("подключались без согласия: %q", modes)
			}
			if p := loadTestFile(t, te).Profiles["home"]; p.Datapath != "" {
				t.Errorf("профиль изменён: datapath = %q", p.Datapath)
			}
		})
	}
}

// tun на этой системе нет вовсе (Windows) — то же предложение socks.
func TestMenuTunUnsupportedOffersSocks(t *testing.T) {
	tunSupport(t, errors.New("datapath tun пока работает только на Linux"))
	te := newMenuEnv(t)
	te.addHome()
	var modes []string
	// План ищет ядро, но подъём подставной: ядро не запускается.
	te.core = "connected"
	te.connectRun = func(_ context.Context, plan *connectPlan) error {
		modes = append(modes, plan.mode)
		return nil
	}
	te.input("\nда\n0\n")

	code, _, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "пока работает только на Linux; на этой системе есть только SOCKS5") ||
		strings.Contains(stderr, "sudo") {
		t.Errorf("подсказка:\n%s", stderr)
	}
	if len(modes) != 1 || modes[0] != config.DatapathSocks {
		t.Errorf("подключения: %q", modes)
	}
}

// ──────────────────────────── сигналы ────────────────────────────

// Ctrl+C во время подключения отменяет только его; вне подключения —
// закрывает меню. SIGTERM и SIGHUP во время подключения тоже отменяют его,
// но меню после этого закрывается.
func TestMenuSignals(t *testing.T) {
	exits := 0
	s := &menuSignals{exit: func() { exits++ }}
	m := &menu{signals: s}

	ctx, done := m.connecting(context.Background())
	s.handle(os.Interrupt)
	if ctx.Err() == nil || exits != 0 || m.leaving() {
		t.Fatalf("Ctrl+C при подключении: отмена %v, выходов %d, закрытие %v", ctx.Err(), exits, m.leaving())
	}
	// Второй Ctrl+C, пока туннель снимается, — выход сразу.
	s.handle(os.Interrupt)
	if exits != 1 {
		t.Errorf("второй Ctrl+C: выходов %d", exits)
	}
	done()

	s.handle(os.Interrupt)
	if exits != 2 {
		t.Errorf("Ctrl+C в меню: выходов %d", exits)
	}

	for _, sig := range []os.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		s := &menuSignals{exit: func() { exits++ }}
		m := &menu{signals: s}
		ctx, done := m.connecting(context.Background())
		s.handle(sig)
		if ctx.Err() == nil || !m.leaving() {
			t.Errorf("%v при подключении: отмена %v, закрытие %v", sig, ctx.Err(), m.leaving())
		}
		done()
	}
}

// ──────────────────────────── конец ввода ────────────────────────────

// На закрытом вводе меню, мастер и каждый вопрос правки кончаются: выходом из
// меню или отменой, а не новым кругом. Секреты читаются строками stdin, как
// из конвейера, — так ввод кончается в одном потоке на любом вопросе. Срок —
// чтобы регрессия падала по времени; вывод и вопросы тестов ограничены
// (maxTestOutput), чтобы цикл не съел память раньше срока.
func TestMenuEndsOnClosedInput(t *testing.T) {
	exhausted := func(context.Context, *connectPlan) error { return &supervisor.HashesExhaustedError{} }
	for _, c := range []struct {
		name  string
		input string
		// home — профиль home в режиме socks (иначе файла нет); tun — в
		// режиме tun без прав.
		home, tun bool
		run       func(context.Context, *connectPlan) error
	}{
		{name: "меню без профилей", input: ""},
		{name: "мастер: ссылка", input: "\n"},
		{name: "мастер: негодная ссылка", input: "\ncsqtt://сломано\n"},
		{name: "мастер: негодное имя", input: "\n\nмой дом\n"},
		{name: "мастер: пустой адрес", input: "\n\n\n\n"},
		{name: "мастер: пустой пароль", input: "\n\n\nexample.org\n\n"},
		{name: "мастер: несовпавший пароль", input: "\n\n\nexample.org\nа\nб\n"},
		{name: "мастер: без ссылок на звонки", input: "\n\n\nexample.org\nа\nа\nhttps://example.org/\n"},
		{name: "мастер: неверный режим", input: "\n\n\nexample.org\nа\nа\n" + testHashA + "\n7\n"},
		{name: "меню с профилем", input: "", home: true},
		{name: "неверный пункт", input: "9\n", home: true},
		{name: "выбор профиля", input: "2\n", home: true},
		{name: "мастер: занятое имя", input: "3\n\nhome\n", home: true},
		{name: "ссылки: что сделать", input: "4\n", home: true},
		{name: "ссылки: новые", input: "4\n2\n", home: true},
		{name: "ссылки: лишние", input: "4\n2\n" + strings.Repeat("manyhash0123456789 ", 7) + "\n", home: true},
		{name: "что заменить", input: "5\n", home: true},
		{name: "пароль", input: "5\n1\n", home: true},
		{name: "повтор пароля", input: "5\n1\nа\n", home: true},
		{name: "адрес", input: "5\n2\nexample.org:порт\n", home: true},
		{name: "удаление", input: "6\n", home: true},
		{name: "звонки закрылись: вопрос", input: "\n", home: true, run: exhausted},
		{name: "звонки закрылись: новые ссылки", input: "\nда\n", home: true, run: exhausted},
		{name: "tun без прав", input: "\n", tun: true, run: exhausted},
	} {
		t.Run(c.name, func(t *testing.T) {
			tunSupport(t, nil)
			runAsRoot(t, false)
			te := newMenuEnv(t)
			switch {
			case c.home:
				te.addHome("-datapath", "socks")
			case c.tun:
				te.addHome()
				te.elevate = func([]string) (bool, error) {
					return false, &noElevationError{reason: "sudo не найден"}
				}
			}
			te.pipe = true
			// Подъём туннеля подставной (connectRun), ядро запускает только
			// проверка звонков.
			te.core = "hashcheck"
			te.connectRun = c.run
			te.input(c.input)

			done := make(chan int, 1)
			var stdout, stderr string
			go func() {
				var code int
				code, stdout, stderr = te.run("menu")
				done <- code
			}()
			select {
			case code := <-done:
				if code != exitOK {
					t.Errorf("код %d, stderr:\n%s", code, stderr)
				}
				if !strings.Contains(stdout, "До свидания") {
					t.Errorf("меню закрылось не концом ввода:\n%s\nstderr:\n%s", stdout, stderr)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("на закрытом вводе меню не кончилось за 10 с")
			}
		})
	}
}
