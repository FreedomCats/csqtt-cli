package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/supervisor"
)

// Проверка звонков — поддельным ядром в режиме --validate-vk-hashes: хеш A
// живой, B — закрытый звонок, C проверить не удалось (main_test.go). Закрытый
// убирается, непроверенный остаётся, новые ссылки добавляются к ним.
func TestMenuUpdateHashesDropsDead(t *testing.T) {
	te := newMenuEnv(t)
	te.addHome()
	te.core = "hashcheck"
	te.answer("https://vk.com/call/join/" + newHash)
	te.input("4\n\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	for _, want := range []string{
		"№1  живой\n",
		"№2  мёртвый — ошибка VK 954: Invalid join link; уберётся",
		"№3  недоступен — request for link=<хеш №3> failed; останется",
		"Закрытых звонков: 1 из 3", "хешей:  3",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("в выводе нет %q:\n%s", want, stdout)
		}
	}
	noSecrets(t, "вывод", stdout+stderr)

	if got := strings.Join(storedHashes(t, te, "home"), " "); got != testHashA+" "+testHashC+" "+newHash {
		t.Errorf("хеши после обновления: %s", got)
	}
}

// Без новых ссылок — только убираются закрытые.
func TestMenuUpdateHashesOnlyRemoves(t *testing.T) {
	te := newMenuEnv(t)
	te.addHome()
	te.core = "hashcheck"
	te.answer("")
	te.input("4\n1\n0\n")

	if code, _, stderr := te.run("menu"); code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if got := strings.Join(storedHashes(t, te, "home"), " "); got != testHashA+" "+testHashC {
		t.Errorf("хеши после обновления: %s", got)
	}
}

// Заменить все: нужна хоть одна ссылка, ответ без ссылок переспрашивается.
func TestMenuUpdateHashesReplaceAll(t *testing.T) {
	te := newMenuEnv(t)
	te.addHome()
	te.core = "hashcheck"
	te.answer("https://example.org/", newHash)
	te.input("4\n2\n0\n")

	code, _, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "Не нашлось ни одной ссылки") {
		t.Errorf("пустой ответ принят:\n%s", stderr)
	}
	if got := strings.Join(storedHashes(t, te, "home"), " "); got != newHash {
		t.Errorf("хеши после замены: %s", got)
	}
}

// Больше шести звонков ядро не берёт: лишнее переспрашивается с учётом
// оставшихся ссылок.
func TestMenuUpdateHashesRoomLeft(t *testing.T) {
	te := newMenuEnv(t)
	te.addHome()
	te.core = "hashcheck"
	var five []string
	for i := range 5 {
		five = append(five, fmt.Sprintf("newhash%025d", i))
	}
	te.answer(strings.Join(five, " "), newHash)
	te.input("4\n1\n0\n")

	code, _, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "Ссылок 5, а поместится 4") {
		t.Errorf("лишние ссылки приняты:\n%s", stderr)
	}
	if got := len(storedHashes(t, te, "home")); got != 3 {
		t.Errorf("хешей %d, ожидалось 3", got)
	}
}

// Проверить не удалось (здесь — ядра нет): ничего не выбрасывается, а
// «Назад» ничего не меняет.
func TestMenuUpdateHashesBackAfterFailedCheck(t *testing.T) {
	te := newMenuEnv(t)
	te.addHome()
	te.input("4\n0\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "Проверить ссылки не удалось: ядра в этом тесте нет") ||
		!strings.Contains(stdout, "Ссылки не изменены") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if got := len(storedHashes(t, te, "home")); got != 3 {
		t.Errorf("хешей %d, ожидалось 3", got)
	}
}

// Конец ввода на вопросе о новых ссылках — ничего не меняется.
func TestMenuUpdateHashesCancelled(t *testing.T) {
	te := newMenuEnv(t)
	te.addHome()
	te.core = "hashcheck"
	te.endOfInput = true
	te.input("4\n2\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "Ссылки не изменены") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if got := len(storedHashes(t, te, "home")); got != 3 {
		t.Errorf("хешей %d, ожидалось 3", got)
	}
}

// Пароль — дважды, несовпадение переспрашивается.
func TestMenuReplacesPassword(t *testing.T) {
	te := newMenuEnv(t)
	te.addHome()
	te.answer("новый", "другой", "новый", "новый")
	te.input("5\n1\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "Пароли не совпали") || !strings.Contains(stdout, "пароль: записан") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	h := loadTestFile(t, te).Profiles["home"]
	if got, _ := te.store.Get(h.PasswordRef); got != "новый" {
		t.Errorf("пароль не заменён: %q", got)
	}
}

// Конец ввода посреди пароля — пароль прежний.
func TestMenuReplacePasswordCancelled(t *testing.T) {
	te := newMenuEnv(t)
	te.addHome()
	te.endOfInput = true
	te.answer("новый")
	te.input("5\n1\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "Пароль не изменён") {
		t.Errorf("stdout:\n%s", stdout)
	}
	h := loadTestFile(t, te).Profiles["home"]
	if got, _ := te.store.Get(h.PasswordRef); got != testPassword {
		t.Errorf("пароль изменён: %q", got)
	}
}

// Неверный адрес переспрашивается; без порта дописывается порт по умолчанию.
func TestMenuReplacesPeer(t *testing.T) {
	te := newMenuEnv(t)
	te.addHome()
	te.input("5\n2\nexample.org:порт\n198.51.100.7\n0\n")

	code, stdout, stderr := te.run("menu")
	if code != exitOK {
		t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "Сейчас: 203.0.113.10:46000") || !strings.Contains(stderr, "Не похоже на адрес") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if got := loadTestFile(t, te).Profiles["home"].Peer; got != "198.51.100.7:46000" {
		t.Errorf("peer = %q", got)
	}
}

// Пустой ответ на скрытый вопрос пункта правки — отмена пункта: Ctrl+D на
// скрытом вводе в терминале не действует, и иначе человек, который передумал,
// застрял бы на вопросе. Профиль и секреты не меняются, меню продолжается.
func TestMenuEditEmptyAnswerCancels(t *testing.T) {
	exhausted := func(context.Context, *connectPlan) error { return &supervisor.HashesExhaustedError{} }
	for _, c := range []struct {
		name  string
		input string
		// core, getErr, run — как в testEnv: ядро, отказ хранилища,
		// подставное подключение.
		core   string
		getErr bool
		run    func(context.Context, *connectPlan) error
		said   string
		prompt string
	}{
		{name: "заменить все ссылки", input: "4\n2\n0\n", core: "hashcheck",
			said: "Ссылки не изменены", prompt: "[отмена]"},
		{name: "добавить, когда ссылок не осталось", input: "4\n1\n0\n", getErr: true,
			said: "Ссылки не изменены", prompt: "[отмена]"},
		{name: "пароль", input: "5\n1\n0\n",
			said: "Пароль не изменён", prompt: "[не менять]"},
		{name: "звонки закрылись", input: "\nда\n0\n", core: "connected", run: exhausted,
			said: "Ссылки не изменены", prompt: "[отмена]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			te := newMenuEnv(t)
			socksHome(t, te)
			te.core = c.core
			runs := 0
			if c.run != nil {
				te.connectRun = func(ctx context.Context, plan *connectPlan) error {
					runs++
					return c.run(ctx, plan)
				}
			}
			if c.getErr {
				te.store.getErr = errors.New("связка недоступна")
			}
			before, err := os.ReadFile(te.configPath())
			if err != nil {
				t.Fatal(err)
			}
			secrets := maps.Clone(te.store.values)
			asked := len(te.asked)
			te.answer("")
			te.input(c.input)

			code, stdout, stderr := te.run("menu")
			if code != exitOK {
				t.Fatalf("код возврата %d, stderr:\n%s", code, stderr)
			}
			if !strings.Contains(stdout, c.said) {
				t.Errorf("не сказано %q:\n%s", c.said, stdout)
			}
			if n := strings.Count(stdout, "1. Подключиться"); n != 2 {
				t.Errorf("меню показано %d раз, ожидалось 2:\n%s", n, stdout)
			}
			if q := te.asked[asked:]; len(q) != 1 || !strings.Contains(q[0], c.prompt) {
				t.Errorf("скрытые вопросы меню: %q, ожидался один с %q", q, c.prompt)
			}
			if runs > 1 {
				t.Errorf("подключений %d: после отмены подключились снова", runs)
			}
			if after, _ := os.ReadFile(te.configPath()); string(after) != string(before) {
				t.Error("файл настроек изменён при отмене")
			}
			if !maps.Equal(te.store.values, secrets) {
				t.Error("секреты изменены при отмене")
			}
		})
	}
}
