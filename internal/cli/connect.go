// connect.go — команда csqtt-cli connect.
//
// Всё сделанное раньше собирается здесь в одну команду: профиль и секреты из
// хранилища → настройки ядра (coreproc.Config), datapath (socks или tun —
// fulltunnel.go), мост капчи, цикл подключения супервизора и вывод хода
// подключения.
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
	"slices"
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

Режим tun (datapath = "tun", умолчание) — системный VPN: весь трафик
системы идёт в туннель через адаптер csqtt-cli. Он требует прав root
(sudo ` + app.Name + ` connect), на Windows — администратора. Файл
настроек, секреты и окно капчи под sudo остаются у того, кто его вызвал. Пока туннель
не поднят, kill-switch блокирует трафик системы; пока поднят, DNS системы
ходит только к резолверам туннеля, а IPv6 наружу закрыт. Не включились
защита DNS или kill-switch — подключения не будет, разве что с -allow-leak.
Сокеты самого ` + app.Name + ` и ядра идут мимо туннеля через физический
интерфейс; сменилась сеть — туннель переподключается.

Режим socks (-datapath socks или datapath = "socks" в профиле) прав не
требует: трафик отдаётся через локальный SOCKS5, и через туннель ходят
программы, настроенные на этот прокси.

В режиме auto_js (vk_hash_mode = "auto_js" в профиле) хеши не нужны: звонок
VK создаёт ядро от имени аккаунта, в который вошли командой ` + app.Name + ` vk login.
При остановке и перед каждым переподключением звонок завершается.

Ход подключения печатается строками в stdout, под ними в терминале — строка
состояния: воркеры, трафик, скорость, аптайм. Капча VK открывается в окне
браузера, а нет окна — решается вручную, вопросы уходят в stderr. Первый
Ctrl+C останавливает туннель. Второй, пока ядро ещё останавливается, убивает
ядро, не дожидаясь его уборки (в auto_js звонок тогда может остаться), а
следующий — сам ` + app.Name + `.

Недоступный звонок исключается из подключения, но профиль не меняется:
заменить хеши — ` + app.Name + ` profile set <профиль> -hashes.`,
		// -net-switch — замер W6 (§06 doc/architecture.md): в обычной
		// помощи его нет.
		hiddenFlags: []string{"net-switch"},
		run:         runConnect,
	}
}

func runConnect(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(connectCommand())
	configPath := addConfigFlag(fs)
	verbose := addVerboseFlag(fs)
	mode := fs.String("datapath", "", "как отдавать трафик: tun (весь трафик системы, нужен root) или socks; "+
		"без флага — из профиля")
	// Выключить на запуск, а не включить: включён он и так, если профиль
	// не выключил.
	noKillSwitch := fs.Bool("no-kill-switch", false, "выключить kill-switch на этот запуск: "+killSwitchUsage)
	// Флаг без поля в профиле: согласие на утечку даётся на один запуск, а не
	// записывается навсегда. Один на защиту DNS и kill-switch: отказывают они
	// по одной причине — nftables.
	allowLeak := fs.Bool("allow-leak", false,
		"tun: подключаться, даже если защита от утечки DNS и kill-switch не включились")
	netSwitch := fs.String("net-switch", netSwitchRestart, "диагностика (замер W6), tun: как переходить в новую сеть — "+
		netSwitchRestart+" (перезапуск ядра) или "+netSwitchBindIf+" (команда BIND_IF работающему ядру)")
	rest, err := parseWithArgs(e, fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 1 {
		return usagef("лишний аргумент: профиль называется один (аргумент не печатается)")
	}
	if *netSwitch != netSwitchRestart && *netSwitch != netSwitchBindIf {
		return usagef("-net-switch: %s или %s", netSwitchRestart, netSwitchBindIf)
	}
	name := ""
	if len(rest) == 1 {
		name = rest[0]
	}

	named := setFlags(fs)
	plan, err := e.planConnect(connectFlags{
		configPath:   *configPath,
		name:         name,
		mode:         *mode,
		modeNamed:    named["datapath"],
		noKillSwitch: named["no-kill-switch"] && *noKillSwitch,
		allowLeak:    *allowLeak,
		netSwitch:    *netSwitch,
	})
	if err != nil {
		return err
	}
	return plan.run(ctx, e, *verbose)
}

// connectFlags — флаги и аргумент connect.
type connectFlags struct {
	configPath, name string
	// mode — -datapath; modeNamed — флаг назван, иначе режим из профиля.
	mode      string
	modeNamed bool
	// noKillSwitch и allowLeak действуют только в режиме tun: у socks нет
	// ни защиты DNS, ни kill-switch, и флаги, как в qwdtt-cli, ему не мешают.
	noKillSwitch, allowLeak bool
	// netSwitch — -net-switch: netSwitchRestart или netSwitchBindIf.
	netSwitch string
}

// connectPlan — всё, что собрано до входа в цикл подключения. Сети здесь
// ещё нет: план можно собрать и выбросить.
type connectPlan struct {
	profile *config.Profile
	// cfg — настройки ядра с паролем и хешами.
	cfg      coreproc.Config
	corePath string
	// mode — config.DatapathSocks или config.DatapathTun.
	mode string

	// allowLeak — -allow-leak: отказ защиты DNS и kill-switch не мешает
	// подключению в tun.
	allowLeak bool
	// killSwitchOff — чем kill-switch выключен, для строки до подключения;
	// пусто — включён.
	killSwitchOff string
	// netSwitch — как переходить в новую сеть (-net-switch).
	netSwitch string
}

// planConnect собирает план: профиль, режим, секреты из хранилища, путь к
// ядру. Профиль без device_id (файл писали руками) записывается заново — Save
// заводит id, иначе ядро представилось бы серверу как «unknown».
func (e *env) planConnect(f connectFlags) (*connectPlan, error) {
	path, file, err := loadProfiles(f.configPath)
	if err != nil {
		return nil, err
	}
	p, err := file.Profile(f.name)
	if err != nil {
		return nil, err
	}

	// Вход VK — до прав root: без него подключения не будет и под sudo, а
	// входят без sudo.
	if p.AutoJS() && !file.VK.LoggedIn() {
		return nil, fmt.Errorf("профиль %s в режиме auto_js: звонок VK создаётся от имени вашего аккаунта, "+
			"а входа в VK нет — войдите: %s vk login", p.Name, app.Name)
	}

	mode := strings.TrimSpace(f.mode)
	if !f.modeNamed {
		mode = config.EffectiveDatapath(p.Datapath)
	}
	switch mode {
	case config.DatapathSocks:
		// В отличие от прочих флагов tun, этот не пропускается молча: за
		// сетью в socks никто не следит, и замер с ним сравнил бы ничто.
		if f.netSwitch == netSwitchBindIf {
			return nil, usagef("-net-switch %s — только с tun: в socks за сменой сети не слежу", f.netSwitch)
		}
	case config.DatapathTun:
		// Права и система — до секретов и до сети: отказ, а не тихий
		// SOCKS5, — человек, ждущий VPN на всю систему, решил бы, что весь
		// его трафик в туннеле.
		from := "в профиле datapath = \"tun\" (умолчание)"
		if f.modeNamed {
			from = "-datapath tun"
		}
		if err := tunRequirementsError(newTun(datapath.TunOptions{}).Requires(), from, p.Name, f.configPath,
			f.modeNamed, f.allowLeak); err != nil {
			return nil, err
		}
	default:
		return nil, usagef("-datapath %q: допустимы %s и %s", f.mode, config.DatapathSocks, config.DatapathTun)
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
	if p.AutoJS() {
		// Хешей в auto_js не нужно, даже если они в профиле остались.
		if cfg.VKToken, err = vkToken(store, file.VK); err != nil {
			return nil, err
		}
	} else {
		raw, err := store.Get(p.VkHashesRef)
		if err != nil {
			return nil, fmt.Errorf("хеши профиля %s: %w", p.Name, err)
		}
		if cfg.Hashes = config.ParseHashes(raw); len(cfg.Hashes) == 0 {
			return nil, fmt.Errorf("в профиле %s нет хешей звонков VK: %s profile set %s -hashes", p.Name, app.Name, p.Name)
		}
	}

	corePath, err := e.findCore()
	if err != nil {
		return nil, err
	}
	plan := &connectPlan{profile: p, cfg: cfg, corePath: corePath, mode: mode, allowLeak: f.allowLeak,
		netSwitch: f.netSwitch}
	switch {
	case f.noKillSwitch:
		plan.killSwitchOff = "-no-kill-switch"
	case !p.KillSwitchOn():
		plan.killSwitchOff = "kill_switch = false в профиле"
	}
	return plan, nil
}

// vkToken читает токен входа VK для auto_js. Не читается или пуст — ошибка
// с подсказкой войти заново; самого токена в ней нет.
func vkToken(store config.SecretStore, vk config.VKSection) (string, error) {
	token, err := store.Get(vk.TokenRef)
	switch {
	case err != nil:
		return "", fmt.Errorf("токен входа VK не читается: %w; если он потерян — войдите заново: %s vk login", err, app.Name)
	case strings.TrimSpace(token) == "":
		return "", fmt.Errorf("токен входа VK пуст — войдите заново: %s vk login", app.Name)
	}
	return strings.TrimSpace(token), nil
}

// isRoot — запущен ли csqtt-cli с правами root, на Windows — администратора
// (isElevated). Переменная ради тестов.
var isRoot = isElevated

// tunRequirementsError объясняет, почему tun не запустится, или возвращает
// nil. Неподдерживаемая система проверяется первой: права тут не помогут, и
// советовать sudo незачем. Затем — защита DNS и kill-switch: где их на
// системе ещё нет (tunGuardMissing), tun только с -allow-leak (allowLeak).
// from — откуда взялся режим tun, для начала строки; modeNamed — tun задан
// флагом -datapath, и в команде с правами он нужен снова: профиль мог бы
// выбрать socks.
func tunRequirementsError(req datapath.Requirements, from, profile, configPath string, modeNamed, allowLeak bool) error {
	args := profile
	if configPath != "" {
		args = "-config " + configPath + " " + profile
	}
	tunArgs := args
	if modeNamed {
		tunArgs = "-datapath tun " + args
	}
	if allowLeak {
		tunArgs = "-allow-leak " + tunArgs
	}
	socks := fmt.Sprintf("%s connect -datapath socks %s (насовсем — %s profile set %s -datapath socks)",
		app.Name, args, app.Name, args)
	if req.Unsupported != nil {
		return &tunRefusal{err: fmt.Errorf("%s: режим tun — весь трафик системы: %w; на этой системе есть только SOCKS5: %s",
			from, req.Unsupported, socks)}
	}
	if req.DNSGuard && tunGuardMissing != nil && !allowLeak {
		withLeak := "-allow-leak " + args
		if modeNamed {
			withLeak = "-allow-leak -datapath tun " + args
		}
		// Прав нет — сказать сразу и о них, а не следующим отказом.
		connectSo := "подключиться так:"
		if req.Admin && !isRoot() {
			connectSo = "подключиться так, " + adminHint
		}
		return &tunRefusal{err: fmt.Errorf("%s: режим tun — весь трафик системы: %w, и DNS-запросы и трафик системы "+
			"могут уходить мимо туннеля; %s %s connect %s; или только SOCKS5: %s",
			from, tunGuardMissing, connectSo, app.Name, withLeak, socks)}
	}
	if req.Admin && !isRoot() {
		return &tunRefusal{needsRoot: true, err: fmt.Errorf("%s: режиму tun — весь трафик системы — %s %s connect %s; "+
			"без прав работает только SOCKS5: %s", from, adminHint, app.Name, tunArgs, socks)}
	}
	return nil
}

// tunRefusal — отказ режима tun до подключения: система его не умеет или
// не хватает прав. Командной строке это обычная ошибка с подсказками, а меню
// по типу узнаёт, что можно предложить socks (menu.go).
type tunRefusal struct {
	err error
	// needsRoot — tun здесь работает, но нужны права root; иначе система его
	// не умеет.
	needsRoot bool
}

func (r *tunRefusal) Error() string { return r.err.Error() }
func (r *tunRefusal) Unwrap() error { return r.err }

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
	workers := cfg.NormalizedWorkers()
	out.header(workers)

	var dp *sessionDatapath
	var err error
	if plan.mode == config.DatapathTun {
		dp, err = plan.openTun(e, out, verbose)
	} else {
		dp, err = openSocks(p, out)
	}
	if err != nil {
		return err
	}
	defer dp.close()

	newBridge := e.captchaBridge
	if newBridge == nil {
		newBridge = func(opts captcha.Options) supervisor.CaptchaFunc { return captcha.NewBridge(opts).Solve }
	}
	solve := newBridge(captcha.Options{
		Out:    out.aside(e.stderr),
		Lines:  e.lines(),
		Open:   e.openURL,
		Dial:   dp.captchaDial,
		Direct: dp.captchaDirect,
		Logf:   out.debugf,
	})
	tunnel, err := supervisor.NewTunnel(supervisor.Options{
		CorePath:     plan.corePath,
		Config:       cfg,
		Link:         dp.link,
		TunFD:        dp.tunFD,
		Datapath:     dp.datapath,
		Captcha:      solve,
		OnMessage:    out.message,
		OnTransition: out.transition,
		Logf:         out.logf,
		KillSwitch:   dp.killSwitch,
		Prepare:      dp.prepare,
	})
	if err != nil {
		return err
	}
	if dp.watch != nil {
		// Снимается раньше datapath: перезапуск после остановки незачем.
		defer dp.watch(tunnel)()
	}
	out.setWorkers(tunnel.Workers)
	out.setConnectedNote(dp.note)

	calls := fmt.Sprintf("хешей %d", len(cfg.Hashes))
	if cfg.AutoJS() {
		calls = "звонок VK создаёт ядро (auto_js)"
	}
	out.line("профиль %s: сервер %s, %s, воркеров %d, %s", p.Name, cfg.Peer, calls, workers, dp.describe)
	switch {
	case workers >= cfg.Workers:
	case cfg.AutoJS():
		out.line("предупреждение: воркеров в профиле %d, а ядро запустит %d (в auto_js не больше %d)",
			cfg.Workers, workers, coreproc.MaxWorkersAutoJS)
	default:
		out.line("предупреждение: воркеров в профиле %d, а ядро запустит %d", cfg.Workers, workers)
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

// sessionDatapath — datapath сеанса и то, что от него нужно циклу
// подключения, мосту капчи и слежению за сетью. Строят его openSocks и
// openTun (fulltunnel.go).
type sessionDatapath struct {
	datapath supervisor.Datapath
	// link или tunFD — канал пакетов с ядром, ровно один.
	link  supervisor.Link
	tunFD supervisor.TunFD
	// describe — для строки профиля, note — для строки «туннель работает».
	describe, note string

	// prepare — supervisor.Options.Prepare: интерфейс и метка ядру на
	// каждую попытку; nil — не нужны (socks).
	prepare func(cfg *coreproc.Config) error
	// killSwitch — supervisor.Options.KillSwitch; nil — kill-switch нет.
	killSwitch func() bool
	// captchaDial и captchaDirect — captcha.Options.Dial и Direct; nil —
	// окно ходит в сеть само, снимать нечего.
	captchaDial   captcha.DialFunc
	captchaDirect func() (done func())
	// watch начинает слежение за сменой сети и возвращает его остановку;
	// nil — следить незачем (socks).
	watch func(sw netSwitcher) (stop func())

	// close убирает всё в обратном порядке.
	close func()
}

// openSocks — datapath socks: слушатель SOCKS5 открывается до запуска ядра,
// чтобы занятый порт выяснился до входа в VK.
func openSocks(p *config.Profile, out *printer) (*sessionDatapath, error) {
	socks, err := datapath.NewSocks(datapath.SocksOptions{Listen: p.Socks.Listen, Logf: out.debugf})
	if err != nil {
		return nil, err
	}
	return &sessionDatapath{
		datapath: socks,
		link:     socks.Link(),
		describe: fmt.Sprintf("SOCKS5 %v", socks.ListenAddr()),
		note:     fmt.Sprintf("SOCKS5 на %v", socks.ListenAddr()),
		close:    func() { _ = socks.Close() },
	}, nil
}

// explainConnectError дописывает к ошибке конца подключения, что делать
// дальше.
func explainConnectError(err error, profile string, dead []int) error {
	var server *supervisor.ServerError
	var exhausted *supervisor.HashesExhaustedError
	var startup *supervisor.StartupError
	var token *supervisor.TokenRejectedError
	var vk *supervisor.VKError
	var lost *datapath.AdapterLostError
	switch {
	case err == nil:
		return nil

	case errors.As(err, &lost):
		return fmt.Errorf("%w\n  туннель снят, трафик системы снова идёт напрямую; подключитесь заново — адаптер "+
			"создастся снова: %s connect %s", err, app.Name, profile)

	case errors.As(err, &token):
		return fmt.Errorf("%w\n  токен отозван, просрочен или не тот — войдите заново: %s vk login", err, app.Name)

	case errors.As(err, &vk):
		return fmt.Errorf("%w\n  попробуйте позже, а если повторяется — войдите заново: %s vk login", err, app.Name)

	case errors.As(err, &exhausted):
		which := ""
		if len(dead) > 0 {
			which = " (" + hashNumbers(dead) + ")"
		}
		return fmt.Errorf("%w\n  звонки профиля %s недоступны%s; профиль не менялся — новые хеши: %s profile set %s -hashes",
			err, profile, which, app.Name, profile)

	case errors.As(err, &server) && server.Rejected():
		return fmt.Errorf("%w\n  %s", err, rejectHint(server.Message, profile))

	case errors.As(err, &startup):
		return fmt.Errorf("%w%s", err, startupHint(startup.Fatal))
	}
	return err
}

// startupHint — подсказка к выходу ядра до PROCESS. О сборке — только когда
// ядро не приняло то, что ему дала оболочка, или причины в журнале нет
// (ошибка разбора аргументов, падение): в режиме с хешами Fatal не
// разбирается и всегда nil. В auto_js ответ VK не того вида — не сборка
// виновата; имя сервера ядро называет само.
func startupHint(fatal *coreproc.FatalError) string {
	switch {
	case fatal == nil || fatal.Kind == coreproc.FatalClient:
		return fmt.Sprintf("\n  ядро %s должно быть из той же сборки, что и %s", coreName, app.Name)
	case fatal.Kind == coreproc.FatalOther:
		return "\n  VK ответил не так, как ждёт ядро, создавая звонок; попробуйте позже"
	}
	return ""
}

// rejectHint — что делать, когда сервер отказал. Причину отказа сервера
// (DENIED:<причина>) ядро передаёт только текстом сообщения: метка FATAL_* и
// фраза по-русски (core/rust-client/protocol.rs). Поэтому различаем по тексту —
// без учёта регистра и по коду причины тоже, на случай если ядро передаст его
// как есть («доступ запрещён (<причина>)»). Отказом (handshake_rejected) ядро
// 2.5.0 считает только причины с меткой: wrong_password, expired, deactivated
// (FATAL_AUTH) и protocol_mismatch (FATAL_PROTOCOL); прочие (server_busy и
// др.) оно пишет в журнал и ждёт дальше. Отказа «пароль у другого
// устройства» у сервера 2.5.0 нет.
func rejectHint(message, profile string) string {
	setPassword := fmt.Sprintf("%s profile set %s -password", app.Name, profile)
	msg := strings.ReplaceAll(strings.ToLower(message), "ё", "е")
	has := func(subs ...string) bool {
		return slices.ContainsFunc(subs, func(s string) bool { return strings.Contains(msg, s) })
	}
	switch {
	case has("fatal_protocol"):
		return "сервер CSQTT другой версии, чем ядро клиента: нужен сервер той же версии (деплой из неё)"
	case !has("fatal_auth"):
		return fmt.Sprintf("неверный пароль (%s) или сервер другой версии", setPassword)
	case has("истек", "expired"):
		return fmt.Sprintf("нужен новый пароль из панели сервера: %s", setPassword)
	case has("неверный пароль", "wrong_password"):
		return fmt.Sprintf("проверьте пароль подключения: %s", setPassword)
	default:
		return fmt.Sprintf("проверьте пароль подключения и его состояние в панели сервера: %s", setPassword)
	}
}

// hashNumbers — «№1, №3».
func hashNumbers(numbers []int) string {
	s := make([]string, len(numbers))
	for i, n := range numbers {
		s[i] = fmt.Sprintf("№%d", n)
	}
	return strings.Join(s, ", ")
}
