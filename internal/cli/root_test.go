package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// runCLI прогоняет команду с буферами вместо потоков процесса.
func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	e := &env{
		stdout: &out,
		stderr: &errOut,
		stdin:  strings.NewReader(""),
		build:  Build{Version: "1.2.3", Commit: "abcdef0"},
	}
	code = run(context.Background(), e, args)
	return code, out.String(), errOut.String()
}

// Запуск без аргументов — вопрос «что ты умеешь», а не ошибка: список команд
// уходит в stdout, код возврата нулевой.
func TestNoArgsListsCommands(t *testing.T) {
	code, stdout, stderr := runCLI()

	if code != exitOK {
		t.Errorf("код возврата = %d, ожидался %d", code, exitOK)
	}
	if stderr != "" {
		t.Errorf("stderr не пуст: %q", stderr)
	}
	for _, cmd := range commands() {
		if !strings.Contains(stdout, cmd.name) {
			t.Errorf("в списке команд нет %q:\n%s", cmd.name, stdout)
		}
		if !strings.Contains(stdout, cmd.summary) {
			t.Errorf("в списке нет описания команды %q:\n%s", cmd.name, stdout)
		}
	}
}

// Каждая команда обязана печатать свои флаги по -h — иначе о них неоткуда
// узнать. Помощь идёт в stdout: её просили.
func TestHelpFlagPrintsFlags(t *testing.T) {
	withFlags := map[string][]string{
		"connect": {"-datapath", "-config", "-v"},
		"profile": {"Подкоманды:", "add", "set", "show", "rm", "list"},
		"hashes":  {"check"},
	}

	for _, cmd := range commands() {
		t.Run(cmd.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(cmd.name, "-h")

			if code != exitOK {
				t.Errorf("код возврата = %d, ожидался %d", code, exitOK)
			}
			if stderr != "" {
				t.Errorf("помощь ушла в stderr: %q", stderr)
			}
			if !strings.Contains(stdout, "Использование: csqtt-cli "+cmd.name) {
				t.Errorf("нет строки синтаксиса:\n%s", stdout)
			}
			for _, name := range withFlags[cmd.name] {
				if !strings.Contains(stdout, name) {
					t.Errorf("нет %s:\n%s", name, stdout)
				}
			}
		})
	}
}

// У подкоманд тоже своя помощь с их флагами.
func TestSubcommandHelp(t *testing.T) {
	cases := map[string][]string{
		"profile add":  {"-link", "-peer", "-workers", "-datapath", "-socks-listen", "-default", "-config"},
		"profile set":  {"-password", "-hashes", "-peer", "-workers", "-config"},
		"profile show": {"-config"},
		"profile rm":   {"-y", "-config"},
		"profile list": {"-config"},
		"hashes check": {"-config", "-v"},
	}
	for name, flags := range cases {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runCLI(append(strings.Fields(name), "-h")...)
			if code != exitOK || stderr != "" {
				t.Errorf("код %d, stderr %q", code, stderr)
			}
			if !strings.Contains(stdout, "Использование: csqtt-cli "+name) {
				t.Errorf("нет строки синтаксиса:\n%s", stdout)
			}
			for _, f := range flags {
				if !strings.Contains(stdout, f) {
					t.Errorf("нет флага %s:\n%s", f, stdout)
				}
			}
		})
	}
}

// csqtt-cli help <команда> печатает то же, что и csqtt-cli <команда> -h.
func TestHelpCommandMatchesHelpFlag(t *testing.T) {
	for _, cmd := range commands() {
		if cmd.name == "help" {
			continue
		}
		codeA, viaHelp, _ := runCLI("help", cmd.name)
		codeB, viaFlag, _ := runCLI(cmd.name, "-h")

		if codeA != exitOK || codeB != exitOK {
			t.Errorf("%s: коды возврата %d и %d, ожидался %d", cmd.name, codeA, codeB, exitOK)
		}
		if viaHelp != viaFlag {
			t.Errorf("%s: csqtt-cli help и -h печатают разное:\n--- help ---\n%s\n--- -h ---\n%s",
				cmd.name, viaHelp, viaFlag)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	code, stdout, stderr := runCLI("конект")

	if code != exitUsage {
		t.Errorf("код возврата = %d, ожидался %d", code, exitUsage)
	}
	if stdout != "" {
		t.Errorf("ошибка ушла в stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "конект") {
		t.Errorf("в сообщении нет набранного имени: %q", stderr)
	}
}

// Неизвестный флаг разбирает пакет flag: он же печатает причину и помощь.
// Наше дело — не напечатать то же самое второй раз и вернуть код 2.
func TestUnknownFlag(t *testing.T) {
	code, stdout, stderr := runCLI("connect", "-нетутакого")

	if code != exitUsage {
		t.Errorf("код возврата = %d, ожидался %d", code, exitUsage)
	}
	if stdout != "" {
		t.Errorf("ошибка ушла в stdout: %q", stdout)
	}
	if n := strings.Count(stderr, "Использование: csqtt-cli connect"); n != 1 {
		t.Errorf("помощь напечатана %d раз(а), ожидался один:\n%s", n, stderr)
	}
}

func TestUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"два профиля", []string{"connect", "home", "work"}, "лишний аргумент"},
		{"version с аргументом", []string{"version", "лишнее"}, "лишний аргумент"},
		{"profile без подкоманды", []string{"profile"}, "не названа подкоманда"},
		{"profile с чужой подкомандой", []string{"profile", "delete"}, "неизвестная подкоманда"},
		{"profile с флагом вместо подкоманды", []string{"profile", "-config", "x"}, "после подкоманды"},
		{"profile show без имени", []string{"profile", "show"}, "не названо имя профиля"},
		{"profile list с именем", []string{"profile", "list", "home"}, "лишний аргумент"},
		{"hashes без check", []string{"hashes"}, "check"},
		{"hashes check с двумя профилями", []string{"hashes", "check", "home", "work"}, "лишний аргумент"},
		{"help с двумя командами", []string{"help", "connect", "profile"}, "лишний аргумент"},
		{"help чужой команды", []string{"help", "конект"}, "неизвестная команда"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(tc.args...)

			if code != exitUsage {
				t.Errorf("код возврата = %d, ожидался %d", code, exitUsage)
			}
			if stdout != "" {
				t.Errorf("ошибка ушла в stdout: %q", stdout)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("в сообщении нет %q:\n%s", tc.want, stderr)
			}
			if !strings.Contains(stderr, "Помощь: csqtt-cli ") {
				t.Errorf("нет подсказки, где смотреть помощь:\n%s", stderr)
			}
		})
	}
}

// Лишний аргумент не повторяется в сообщении: это может быть секрет.
func TestExtraArgumentNotEchoed(t *testing.T) {
	for _, args := range [][]string{
		{"connect", "home", "секрет-лишний"},
		{"profile", "show", "home", "секрет-лишний"},
		{"profile", "set", "home", "-password", "секрет-лишний"},
		{"hashes", "check", "home", "секрет-лишний"},
	} {
		_, _, stderr := runCLI(args...)
		if strings.Contains(stderr, "секрет-лишний") {
			t.Errorf("%q: аргумент повторён:\n%s", args, stderr)
		}
	}
}

func TestVersion(t *testing.T) {
	code, stdout, stderr := runCLI("version")

	if code != exitOK {
		t.Errorf("код возврата = %d, ожидался %d", code, exitOK)
	}
	if stderr != "" {
		t.Errorf("stderr не пуст: %q", stderr)
	}
	if !strings.HasPrefix(stdout, "csqtt-cli 1.2.3 (abcdef0)\n") {
		t.Errorf("первая строка версии:\n%s", stdout)
	}
}

// Флаги-синонимы помощи на месте команды показывают общий список, а не ищутся
// среди команд.
func TestTopLevelHelpFlags(t *testing.T) {
	for _, arg := range []string{"-h", "-help", "--help", "help"} {
		code, stdout, stderr := runCLI(arg)

		if code != exitOK {
			t.Errorf("%s: код возврата = %d, ожидался %d", arg, code, exitOK)
		}
		if stderr != "" {
			t.Errorf("%s: stderr не пуст: %q", arg, stderr)
		}
		if !strings.Contains(stdout, "Команды:") {
			t.Errorf("%s: нет списка команд:\n%s", arg, stdout)
		}
	}
}

// parseWithArgs разбирает флаги и после имени профиля, а после "--" всё —
// значения.
func TestParseWithArgs(t *testing.T) {
	fs := newFlagSet(connectCommand())
	verbose := addVerboseFlag(fs)
	e := &env{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	rest, err := parseWithArgs(e, fs, []string{"home", "-v", "--", "-x"})
	if err != nil || !*verbose || len(rest) != 2 || rest[0] != "home" || rest[1] != "-x" {
		t.Errorf("parseWithArgs = %q, %v, -v = %v", rest, err, *verbose)
	}
}

// rejectSecretValues отличает значение секрета от явного true/false.
func TestRejectSecretValues(t *testing.T) {
	cmd := profileSetCommand()
	for _, args := range [][]string{{"-password"}, {"-password=true"}, {"--hashes=false"}, {"--", "-password=x"}, {"-peer=x"}} {
		if err := rejectSecretValues(cmd, args, "password", "hashes"); err != nil {
			t.Errorf("%q: %v", args, err)
		}
	}
	for _, args := range [][]string{{"-password=x"}, {"--hashes=abc"}, {"home", "-password=", "-v"}} {
		if err := rejectSecretValues(cmd, args, "password", "hashes"); err == nil {
			t.Errorf("%q: секрет пропущен", args)
		}
	}
}
