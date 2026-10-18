// connect.go — команда csqtt-cli connect.
//
// Всё сделанное раньше собирается здесь в одну команду: профиль и секреты из
// хранилища → настройки ядра (coreproc.Config), datapath socks, мост капчи,
// цикл подключения супервизора и вывод хода подключения.
//
// Сборка отделена от запуска: planConnect ничего не трогает в сети и
// возвращает готовый план, а run уже поднимает туннель.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/app"
	"github.com/FreedomCats/csqtt-cli/internal/captcha"
	"github.com/FreedomCats/csqtt-cli/internal/config"
	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
	"github.com/FreedomCats/csqtt-cli/internal/supervisor"
)

// coreName — имя бинаря ядра. Ищется только рядом с csqtt-cli (Р5): ни
// текущий каталог, ни PATH не подходят — там мог оказаться чужой файл.
var coreName = coreFileName()

func coreFileName() string {
	if runtime.GOOS == "windows" {
		return "csqtt-core.exe"
	}
	return "csqtt-core"
}

// statusRefresh — как часто перерисовывать строку состояния: аптайм идёт и
// без событий.
const statusRefresh = time.Second

func connectCommand() *command {
	return &command{
		name:    "connect",
		args:    "[флаги] [профиль]",
		summary: "подключиться к серверу CSQTT",
		description: `Настройки берутся из профиля в profiles.toml. Имя профиля можно не называть,
если в файле он один или назван в default_profile. Пароль и хеши звонков VK
лежат в хранилище секретов; записывает их ` + app.Name + ` profile add.

Сейчас трафик отдаётся только через локальный SOCKS5 (-datapath socks или
datapath = "socks" в профиле): через туннель ходят программы, настроенные на
этот прокси. Режим tun — весь трафик системы — появится в следующих версиях.

Ход подключения печатается строками в stdout, под ними в терминале — строка
состояния: воркеры, трафик, скорость, аптайм. Капча VK открывается в окне
браузера, а нет окна — решается вручную, вопросы уходят в stderr. Первый
Ctrl+C останавливает туннель, второй убивает процесс.

Недоступный звонок исключается из подключения, но профиль не меняется:
заменить хеши — ` + app.Name + ` profile set <профиль> -hashes.`,
		run: runConnect,
	}
}

func runConnect(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(connectCommand())
	configPath := addConfigFlag(fs)
	verbose := addVerboseFlag(fs)
	mode := fs.String("datapath", "", "как отдавать трафик: socks; без флага — из профиля")
	rest, err := parseWithArgs(e, fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 1 {
		return usagef("лишний аргумент: профиль называется один (аргумент не печатается)")
	}
	name := ""
	if len(rest) == 1 {
		name = rest[0]
	}

	plan, err := e.planConnect(*configPath, name, *mode, setFlags(fs)["datapath"])
	if err != nil {
		return err
	}
	return plan.run(ctx, e, *verbose)
}

// connectPlan — всё, что собрано до входа в цикл подключения. Сети здесь
// ещё нет: план можно собрать и выбросить.
type connectPlan struct {
	profile *config.Profile
	// cfg — настройки ядра с паролем и хешами.
	cfg      coreproc.Config
	corePath string
}

// planConnect собирает план: профиль, режим, секреты из хранилища, путь к
// ядру. Профиль без device_id (файл писали руками) записывается заново — Save
// заводит id, иначе ядро представилось бы серверу как «unknown».
func (e *env) planConnect(configPath, name, mode string, modeNamed bool) (*connectPlan, error) {
	path, file, err := loadProfiles(configPath)
	if err != nil {
		return nil, err
	}
	p, err := file.Profile(name)
	if err != nil {
		return nil, err
	}

	if !modeNamed {
		mode = config.EffectiveDatapath(p.Datapath)
	}
	switch strings.TrimSpace(mode) {
	case config.DatapathSocks:
	case config.DatapathTun:
		// Отказ, а не тихий SOCKS5: человек, ждущий VPN на всю систему,
		// решил бы, что весь его трафик в туннеле.
		from := "в профиле datapath = \"tun\" (умолчание)"
		if modeNamed {
			from = "-datapath tun"
		}
		return nil, fmt.Errorf("%s: режим tun — весь трафик системы — появится в следующих версиях, "+
			"сейчас только SOCKS5: %s connect -datapath socks %s, или запишите в профиль: %s profile set %s -datapath socks",
			from, app.Name, p.Name, app.Name, p.Name)
	default:
		return nil, usagef("-datapath %q: допустимы %s и %s", mode, config.DatapathSocks, config.DatapathTun)
	}

	if strings.TrimSpace(p.DeviceID) == "" {
		if err := config.Save(path, file); err != nil {
			return nil, fmt.Errorf("device_id профиля: %w", err)
		}
		fmt.Fprintf(e.stdout, "В профиле %s не было device_id — заведён и записан: %s\n", p.Name, path)
	}

	store := e.secretsFor(path)
	cfg := p.Core()
	if cfg.Password, err = store.Get(p.PasswordRef); err != nil {
		return nil, fmt.Errorf("пароль профиля %s: %w", p.Name, err)
	}
	raw, err := store.Get(p.VkHashesRef)
	if err != nil {
		return nil, fmt.Errorf("хеши профиля %s: %w", p.Name, err)
	}
	if cfg.Hashes = config.ParseHashes(raw); len(cfg.Hashes) == 0 {
		return nil, fmt.Errorf("в профиле %s нет хешей звонков VK: %s profile set %s -hashes", p.Name, app.Name, p.Name)
	}

	corePath, err := e.findCore()
	if err != nil {
		return nil, err
	}
	return &connectPlan{profile: p, cfg: cfg, corePath: corePath}, nil
}

// findCore — путь к csqtt-core рядом с csqtt-cli.
func (e *env) findCore() (string, error) {
	if e.corePath != nil {
		return e.corePath()
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("не найден каталог %s: %w", app.Name, err)
	}
	path := filepath.Join(filepath.Dir(exe), coreName)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("ядра нет: %s должен лежать рядом с %s (%v)", coreName, app.Name, err)
	}
	return path, nil
}

// run поднимает туннель и держит его до Ctrl+C или до конца, после которого
// продолжать бессмысленно.
func (plan *connectPlan) run(ctx context.Context, e *env, verbose bool) error {
	p, cfg := plan.profile, plan.cfg

	out := newPrinter(e.stdout, verbose)
	if width, ok := e.stdoutTerminal(); ok {
		out.enableStatus(width)
	}
	out.setHashes(cfg.Hashes)
	workers := coreproc.NormalizeWorkers(cfg.Workers, len(cfg.Hashes))
	out.header(workers)

	socks, err := datapath.NewSocks(datapath.SocksOptions{Listen: p.Socks.Listen, Logf: out.debugf})
	if err != nil {
		return err
	}
	defer socks.Close()

	bridge := captcha.NewBridge(captcha.Options{
		Out:   out.aside(e.stderr),
		Lines: e.lines(),
		Open:  e.openURL,
		Logf:  out.debugf,
	})
	tunnel, err := supervisor.NewTunnel(supervisor.Options{
		CorePath:     plan.corePath,
		Config:       cfg,
		Link:         socks.Link(),
		Datapath:     socks,
		Captcha:      bridge.Solve,
		OnMessage:    out.message,
		OnTransition: out.transition,
		Logf:         out.logf,
	})
	if err != nil {
		return err
	}
	out.setWorkers(tunnel.Workers)
	out.setConnectedNote(fmt.Sprintf("SOCKS5 на %v", socks.ListenAddr()))

	out.line("профиль %s: сервер %s, хешей %d, воркеров %d, SOCKS5 %v",
		p.Name, cfg.Peer, len(cfg.Hashes), workers, socks.ListenAddr())
	if workers < cfg.Workers {
		out.line("предупреждение: воркеров в профиле %d, а на %d хеш(ах) ядро запустит %d (не больше 27 на хеш)",
			cfg.Workers, len(cfg.Hashes), workers)
	}

	stopRefresh := out.refreshEvery(statusRefresh)
	err = tunnel.Run(ctx)
	stopRefresh()
	out.finish()

	dead := out.deadHashes()
	if err == nil {
		out.line("остановлено")
	}
	var exhausted *supervisor.HashesExhaustedError
	if len(dead) > 0 && !errors.As(err, &exhausted) {
		out.line("недоступные звонки: %s; профиль не менялся — заменить хеши: %s profile set %s -hashes",
			hashNumbers(dead), app.Name, p.Name)
	}
	return explainConnectError(err, p.Name, dead)
}

// explainConnectError дописывает к ошибке конца подключения, что делать
// дальше.
func explainConnectError(err error, profile string, dead []int) error {
	var server *supervisor.ServerError
	var exhausted *supervisor.HashesExhaustedError
	var startup *supervisor.StartupError
	switch {
	case err == nil:
		return nil

	case errors.As(err, &exhausted):
		which := ""
		if len(dead) > 0 {
			which = " (" + hashNumbers(dead) + ")"
		}
		return fmt.Errorf("%w\n  звонки профиля %s недоступны%s; профиль не менялся — новые хеши: %s profile set %s -hashes",
			err, profile, which, app.Name, profile)

	case errors.As(err, &server) && server.Rejected():
		var hint string
		switch {
		case strings.Contains(server.Message, "FATAL_PROTOCOL"):
			hint = "сервер CSQTT другой версии, чем ядро клиента: нужен сервер той же версии (деплой из неё)"
		case strings.Contains(server.Message, "FATAL_AUTH"):
			hint = fmt.Sprintf("проверьте пароль подключения: %s profile set %s -password", app.Name, profile)
		default:
			hint = fmt.Sprintf("неверный пароль (%s profile set %s -password) или сервер другой версии", app.Name, profile)
		}
		return fmt.Errorf("%w\n  %s", err, hint)

	case errors.As(err, &startup):
		return fmt.Errorf("%w\n  ядро %s должно быть из той же сборки, что и %s", err, coreName, app.Name)
	}
	return err
}

// hashNumbers — «№1, №3».
func hashNumbers(numbers []int) string {
	s := make([]string, len(numbers))
	for i, n := range numbers {
		s[i] = fmt.Sprintf("№%d", n)
	}
	return strings.Join(s, ", ")
}
