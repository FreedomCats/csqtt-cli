// Package cli — разбор командной строки csqtt-cli и запуск того, что за ней
// стоит.
//
// Разбор идёт стандартным flag с подкомандами, без внешних библиотек: команд
// немного, флаги у них простые, а чужая библиотека принесла бы с собой свой
// формат помощи, свои соглашения о кодах возврата и ещё одну зависимость,
// которую пришлось бы обновлять.
//
// Здесь только каркас: список команд, общие флаги, помощь и коды возврата.
// Тела команд лежат рядом — connect.go, hashes.go, profile.go, vk.go, а меню для
// тех, кто команд не набирает, — menu.go.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	csqttcli "github.com/FreedomCats/csqtt-cli"
	"github.com/FreedomCats/csqtt-cli/internal/app"
	"github.com/FreedomCats/csqtt-cli/internal/captcha"
	"github.com/FreedomCats/csqtt-cli/internal/config"
	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/lines"
	"github.com/FreedomCats/csqtt-cli/internal/supervisor"
)

// Коды возврата. Три, и разделены они намеренно: скрипту важно отличить
// «команда набрана неверно» от «команда отработала и не смогла».
const (
	exitOK    = 0 // всё получилось
	exitError = 1 // команда отработала и вернула ошибку
	exitUsage = 2 // ошибка в командной строке: нет такой команды, флага, аргумента
)

// Build — сведения о сборке. Подставляются в cmd/csqtt-cli через -ldflags
// (задача I3).
type Build struct {
	Version string
	Commit  string
}

// env — окружение команды: потоки ввода-вывода, сведения о сборке и то, что
// тесты подменяют.
//
// Отдельной структурой ради тестов: они подставляют буферы вместо os.Stdout
// и os.Stderr, а команды никуда, кроме env, не пишут.
type env struct {
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader
	build  Build

	// secrets подменяет скрытый ввод. Пусто — спрашиваем у терминала
	// (см. prompt.go); заполняется в тестах, где терминала нет.
	secrets func(prompt string) (string, error)
	// input — общий читатель строк stdin: вопросы, пауза и ручная капча
	// читают через него (lines.Reader). Заводится при первом чтении строки.
	input *lines.Reader
	// openURL открывает ссылку в браузере — для ручного режима капчи. Пусто —
	// ссылка только печатается; так в тестах, где чужие программы не запускают.
	openURL func(rawURL string) error
	// corePath подменяет поиск csqtt-core рядом с csqtt-cli. Пусто — рядом
	// (connect.go); подменяют тесты, где ядро — поддельное.
	corePath func() (string, error)
	// captchaBridge подменяет мост капчи (captcha.NewBridge): тесты смотрят,
	// с чем его собрали. Пусто — настоящий.
	captchaBridge func(opts captcha.Options) supervisor.CaptchaFunc
	// vkLogin подменяет окно входа VK (captcha.NewLoginWindow): тесты не
	// открывают браузер. Пусто — окно этой системы.
	vkLogin func(opts captcha.LoginOptions) (captcha.LoginWindow, error)
	// secretStore подменяет хранилище секретов рядом с файлом настроек.
	// Пусто — config.NewSecretStoreFor; подменяют тесты.
	secretStore func(configPath string) config.SecretStore
	// hashCheck подменяет coreproc.CheckHashes. Пусто — настоящая проверка.
	hashCheck func(ctx context.Context, corePath string, cfg coreproc.Config, onLog func(coreproc.Log)) ([]coreproc.HashCheck, error)
	// connectRun подменяет подъём туннеля по готовому плану — для меню
	// (menu.go). Пусто — plan.run; подменяют тесты, где сети нет.
	connectRun func(ctx context.Context, plan *connectPlan) error
	// elevate подменяет перезапуск с правами (elevate.go): тесты не зовут
	// sudo и UAC. Пусто — настоящий перезапуск.
	elevate func(args []string) (detached bool, err error)

	// tty подменяет проверку «stdin и stdout — терминал», от которой зависит,
	// откроется ли меню (menu.go). Пусто — проверяются настоящие потоки.
	tty func() bool
	// signals — сигналы меню перехватывает оно само: так в настоящем
	// процессе (Main), но не в тестах, где сигналов нет.
	signals bool
	// noPause — пауза перед закрытием окна не нужна: человек сам ушёл из
	// меню (pause.go).
	noPause bool
	// ownsConsole подменяет проверку «csqtt-cli один в своей консоли, и её
	// видит человек» (pause.go). Пусто — настоящая проверка.
	ownsConsole func() bool
	// serverSilence — supervisor.Options.ServerSilence: сколько connect
	// ждёт ответа сервера. Ноль — умолчание супервизора; короче — в тестах.
	serverSilence time.Duration
}

// command — одна команда csqtt-cli.
//
// Своего набора флагов у команды здесь нет: его заводит сама run, потому что
// разбор и значения флагов нужны в одном месте. Помощь по команде печатается
// тем же путём — help зовёт run с аргументом -h.
type command struct {
	// name — как команда называется в командной строке.
	name string
	// args — строка синтаксиса после имени команды, для строки «Использование».
	args string
	// summary — одна строка для списка команд. С маленькой буквы, без точки.
	summary string
	// description — абзац между строкой синтаксиса и списком флагов.
	// Необязателен.
	description string
	// hiddenFlags — флаги, которых нет в помощи: диагностические, их
	// описывает doc/architecture.md.
	hiddenFlags []string
	// run разбирает свои аргументы и делает работу.
	run func(ctx context.Context, e *env, args []string) error
}

// commands — все команды csqtt-cli в порядке, в котором их показывает помощь:
// сначала то, ради чего программу запускают, потом обслуживающее.
func commands() []*command {
	return []*command{
		connectCommand(),
		menuCommand(),
		profileCommand(),
		hashesCommand(),
		vkCommand(),
		versionCommand(),
		helpCommand(),
	}
}

func lookup(name string) *command {
	for _, cmd := range commands() {
		if cmd.name == name {
			return cmd
		}
	}
	return nil
}

// Main — точка входа CLI. Возвращает код возврата; os.Exit зовёт cmd/csqtt-cli.
func Main(args []string, build Build) int {
	// Служебный запуск самого csqtt-cli от имени вызвавшего sudo — не
	// команда человека: ни меню, ни паузы перед выходом, ни обработки Ctrl+C
	// (secrethelper_linux.go).
	if code, ok := runSecretHelper(args, os.Stdin, os.Stdout, os.Stderr); ok {
		return code
	}

	e := &env{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		stdin:   os.Stdin,
		build:   build,
		openURL: captcha.OpenInBrowser,
		signals: true,
	}
	defer pauseBeforeExit(e)

	// Меню разбирает сигналы само: Ctrl+C во время подключения возвращает
	// в него, а не завершает процесс (menu.go).
	if wantsMenu(e, args) {
		return run(context.Background(), e, args)
	}

	// Первый Ctrl+C, SIGTERM или SIGHUP (если он не игнорируется, как под
	// nohup) отменяет контекст команды. Второй во время остановки туннеля
	// убивает только ядро, не дожидаясь его уборки, а csqtt-cli доводит свою
	// (маршруты, DNS, kill-switch); третий — или второй, когда прерывать
	// нечего, — завершает процесс (supervisor.Interrupt). Ждать корректной
	// остановки дважды пользователь не обязан, а на зависшем ядре она бы и
	// не помогла.
	ctx, stop := supervisor.Interrupt(context.Background())
	defer stop()

	return run(ctx, e, args)
}

// run разделён с Main ради тестов: им нужны свои потоки и свой контекст.
func run(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		// В терминале запуск без аргументов — это человек, которому нужно
		// меню: на Windows так выглядит двойной щелчок по csqtt-cli.exe.
		if e.console() {
			return report(e, menuCommand(), runMenu(ctx, e, nil))
		}
		// Иначе — вопрос «что ты умеешь», а не ошибка, поэтому список
		// команд уходит в stdout и код возврата нулевой.
		printUsage(e.stdout)
		return exitOK
	}

	// Ссылка csqtt:// несёт пароль и хеши: в аргументе она попала бы в
	// историю оболочки и в список процессов. Её место — скрытый ввод, и
	// повторять её в сообщении нельзя.
	if hasShareLink(args) {
		fmt.Fprintf(e.stderr, "%s: ссылка csqtt:// в аргументах не принимается — в ней пароль; "+
			"она вводится скрытым вводом: %s profile add <имя> -link (у имеющегося профиля — profile set <имя> -link)\n",
			app.Name, app.Name)
		return exitUsage
	}

	name := args[0]
	if isHelpFlag(name) {
		printUsage(e.stdout)
		return exitOK
	}

	cmd := lookup(name)
	if cmd == nil {
		fmt.Fprintf(e.stderr, "%s: неизвестная команда %q\n", app.Name, name)
		fmt.Fprintf(e.stderr, "Список команд: %s help\n", app.Name)
		return exitUsage
	}

	return report(e, cmd, cmd.run(ctx, e, args[1:]))
}

// hasShareLink — среди аргументов есть ссылка csqtt://.
func hasShareLink(args []string) bool {
	for _, arg := range args {
		if strings.Contains(strings.ToLower(arg), "csqtt://") {
			return true
		}
	}
	return false
}

// report превращает ошибку команды в код возврата и печатает то, о чём ещё
// не сказано.
func report(e *env, cmd *command, err error) int {
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, flag.ErrHelp):
		// Помощь просили — значит, её печать и есть успешный исход.
		return exitOK
	case errors.Is(err, errReported):
		// Пакет flag уже напечатал и ошибку, и помощь.
		return exitUsage
	}

	// Подкоманда называет себя сама: ошибку в аргументах `csqtt-cli profile set`
	// нельзя подписывать именем `profile` — подсказка отправила бы не туда.
	name := cmd.name
	var ue usageError
	isUsage := errors.As(err, &ue)
	if isUsage && ue.cmd != "" {
		name = ue.cmd
	}

	if errors.Is(err, context.Canceled) {
		fmt.Fprintf(e.stderr, "%s %s: прервано\n", app.Name, name)
		return exitError
	}
	fmt.Fprintf(e.stderr, "%s %s: %v\n", app.Name, name, err)
	if isUsage {
		fmt.Fprintf(e.stderr, "Помощь: %s %s -h\n", app.Name, name)
		return exitUsage
	}
	return exitError
}

// usageError — ошибка в командной строке, о которой ещё не сказано: лишний
// аргумент, отсутствующая подкоманда, несовместимые флаги. От обычной ошибки
// отличается кодом возврата и подсказкой, где смотреть помощь.
type usageError struct {
	msg string
	// cmd — чьи это аргументы. Пусто означает «команды верхнего уровня»;
	// подкоманда ставит сюда своё полное имя вида "profile set".
	cmd string
}

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error {
	return usageError{msg: fmt.Sprintf(format, a...)}
}

// subUsagef — то же для подкоманды: подсказка обязана называть её, а не
// команду верхнего уровня.
func subUsagef(cmd *command, format string, a ...any) error {
	return usageError{msg: fmt.Sprintf(format, a...), cmd: cmd.name}
}

// errReported — ошибка, о которой уже сказано пользователю. Такую печатать
// второй раз нельзя: пакет flag сам пишет и причину, и помощь по команде.
var errReported = errors.New("ошибка в аргументах команды")

// newFlagSet заводит набор флагов команды с нашей помощью вместо стандартной.
func newFlagSet(cmd *command) *flag.FlagSet {
	fs := flag.NewFlagSet(app.Name+" "+cmd.name, flag.ContinueOnError)
	fs.Usage = func() { writeUsage(cmd, fs) }
	return fs
}

// parse разбирает флаги команды.
//
// Помощь по -h уходит в stdout, сообщение об ошибке — в stderr: помощь просили,
// ошибку — нет. Пакет flag пишет и то и другое в один поток, поэтому поток
// выбирается заранее, по самим аргументам.
func parse(e *env, fs *flag.FlagSet, args []string) error {
	if wantsHelp(args) {
		fs.SetOutput(e.stdout)
	} else {
		fs.SetOutput(e.stderr)
	}

	err := fs.Parse(args)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, flag.ErrHelp):
		return err
	default:
		return fmt.Errorf("%w: %w", errReported, err)
	}
}

// parseWithArgs разбирает флаги команды и возвращает позиционные аргументы.
//
// Стандартный flag останавливается на первом же не-флаге, поэтому
// `csqtt-cli profile set home -peer …` он разобрал бы как имя профиля и три
// лишних аргумента. Записывать флаги перед именем — не то, чего ждут от
// команды, у которой имя главное слово, поэтому разбор продолжается за каждым
// позиционным аргументом.
//
// Значения после "--" остаются значениями: там разбор прекращается совсем.
func parseWithArgs(e *env, fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := parse(e, fs, args); err != nil {
			return nil, err
		}

		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		if stoppedAtTerminator(args, rest) {
			return append(positional, rest...), nil
		}

		positional = append(positional, rest[0])
		if len(rest) == 1 {
			return positional, nil
		}
		args = rest[1:]
	}
}

// stoppedAtTerminator — разбор остановился на "--", а не на обычном аргументе.
// Сам разделитель flag съедает, поэтому он стоит ровно перед остатком.
func stoppedAtTerminator(args, rest []string) bool {
	i := len(args) - len(rest) - 1
	return i >= 0 && args[i] == "--"
}

// setFlags возвращает имена флагов, которые действительно назвали в командной
// строке. Нужно, чтобы отличить «-workers 0» от «-workers не задан»: профиль
// меняется только тем, что названо.
func setFlags(fs *flag.FlagSet) map[string]bool {
	named := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { named[f.Name] = true })
	return named
}

// rejectSecretValues отказывает, если флагу-вопросу о секрете (-password,
// -hashes, -link) дали значение: `-password=секрет`. Сами эти флаги только
// просят спросить секрет скрытым вводом, а значение в командной строке попало
// бы в историю оболочки и в список процессов.
//
// Проверка — до пакета flag: тот на `-password=секрет` ответил бы «invalid
// boolean value "секрет"», то есть напечатал бы сам секрет. Здесь значение
// не печатается.
func rejectSecretValues(cmd *command, args []string, names ...string) error {
	for _, arg := range args {
		if arg == "--" {
			return nil
		}
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name, value, ok := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !ok {
			continue
		}
		for _, secret := range names {
			if name != secret {
				continue
			}
			switch strings.ToLower(value) {
			case "true", "false", "1", "0", "t", "f":
				continue
			}
			return subUsagef(cmd, "-%s значения не принимает: секрет вводится скрытым вводом, "+
				"а не в командной строке (значение не печатается)", secret)
		}
	}
	return nil
}

func isHelpFlag(arg string) bool {
	return arg == "-h" || arg == "-help" || arg == "--help"
}

// wantsHelp — просили ли помощь. Аргументы после "--" не считаются: там уже
// не флаги, а значения.
func wantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if isHelpFlag(arg) {
			return true
		}
	}
	return false
}

// printUsage печатает список команд — то, что видно при запуске без аргументов.
func printUsage(w io.Writer) {
	fmt.Fprintf(w, "%s — консольный клиент CSQTT: VPN поверх звонков VK.\n", app.Name)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Использование: %s <команда> [флаги] [аргументы]\n", app.Name)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Команды:")
	for _, cmd := range commands() {
		fmt.Fprintf(w, "  %-9s %s\n", cmd.name, cmd.summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Без команды в терминале открывается меню (%s menu): подключение и профили вопросами.\n", app.Name)
	fmt.Fprintf(w, "Помощь по команде: %s help <команда> или %s <команда> -h\n", app.Name, app.Name)
}

// writeUsage печатает помощь по одной команде: строку синтаксиса, описание
// и флаги. Зовётся пакетом flag через fs.Usage, поэтому пишет туда же, куда
// пишет сам набор флагов.
func writeUsage(cmd *command, fs *flag.FlagSet) {
	w := fs.Output()

	fmt.Fprintf(w, "Использование: %s %s", app.Name, cmd.name)
	if cmd.args != "" {
		fmt.Fprintf(w, " %s", cmd.args)
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "\n%s\n", capitalize(cmd.summary))
	if cmd.description != "" {
		fmt.Fprintf(w, "\n%s\n", strings.TrimRight(cmd.description, "\n"))
	}

	shown := visibleFlags(cmd, fs)
	if hasFlags(shown) {
		fmt.Fprintln(w, "\nФлаги:")
		shown.SetOutput(w)
		shown.PrintDefaults()
	}
}

// visibleFlags — флаги команды для помощи: без cmd.hiddenFlags. Копия
// набора, а не он сам: пакет flag прятать флаги не умеет.
func visibleFlags(cmd *command, fs *flag.FlagSet) *flag.FlagSet {
	if len(cmd.hiddenFlags) == 0 {
		return fs
	}
	shown := flag.NewFlagSet(fs.Name(), flag.ContinueOnError)
	fs.VisitAll(func(f *flag.Flag) {
		if slices.Contains(cmd.hiddenFlags, f.Name) {
			return
		}
		shown.Var(f.Value, f.Name, f.Usage)
		// Умолчание — прежнее: значение к этому времени мог задать разбор.
		shown.Lookup(f.Name).DefValue = f.DefValue
	})
	return shown
}

func hasFlags(fs *flag.FlagSet) bool {
	found := false
	fs.VisitAll(func(*flag.Flag) { found = true })
	return found
}

// capitalize поднимает первую букву: summary пишется строчной для ровного
// списка команд, а в помощи по команде это отдельное предложение.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// --- общие флаги ---

func addConfigFlag(fs *flag.FlagSet) *string {
	return fs.String("config", "", "путь к profiles.toml вместо файла настроек по умолчанию")
}

func addVerboseFlag(fs *flag.FlagSet) *bool {
	return fs.Bool("v", false, "подробно: журнал ядра, SOCKS5 и капчи, а не только значимые события")
}

// --- version ---

func versionCommand() *command {
	return &command{
		name:    "version",
		summary: "версия и коммит сборки, версия ядра CSQTT",
		run:     runVersion,
	}
}

func runVersion(_ context.Context, e *env, args []string) error {
	fs := newFlagSet(versionCommand())
	if err := parse(e, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usagef("лишний аргумент: команда version аргументов не принимает")
	}

	fmt.Fprintf(e.stdout, "%s %s (%s)\n", app.Name, e.build.Version, e.build.Commit)
	fmt.Fprintf(e.stdout, "ядро: CSQTT %s — сервер CSQTT нужен той же версии\n", csqttcli.CoreVersion)
	fmt.Fprintf(e.stdout, "сборка: %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return nil
}

// --- help ---

func helpCommand() *command {
	return &command{
		name:    "help",
		args:    "[команда]",
		summary: "помощь по команде",
		run:     runHelp,
	}
}

func runHelp(ctx context.Context, e *env, args []string) error {
	// Свой набор флагов у help пустой, но заводится он на общих основаниях:
	// иначе `csqtt-cli help -h` искал бы команду с именем «-h».
	fs := newFlagSet(helpCommand())
	if err := parse(e, fs, args); err != nil {
		return err
	}

	if fs.NArg() == 0 {
		printUsage(e.stdout)
		return nil
	}
	if fs.NArg() > 1 {
		return usagef("лишний аргумент: help принимает не больше одного имени команды")
	}

	cmd := lookup(fs.Arg(0))
	if cmd == nil {
		return usagef("неизвестная команда %q; список команд: %s help", fs.Arg(0), app.Name)
	}

	// Помощь печатает сама команда: набор флагов заводится в её run, и другого
	// способа добраться до него нет. Возвращённый flag.ErrHelp наверху считается
	// успехом — см. report.
	return cmd.run(ctx, e, []string{"-h"})
}
