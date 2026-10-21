// menu.go — интерактивный режим: csqtt-cli без аргументов в терминале или
// команда menu.
//
// Консольные команды сложны тем, кто их не набирает: на Windows csqtt-cli
// запускают двойным щелчком, и окно консоли — всё, что видит человек. Меню
// ведёт его вопросами и само зовёт то же, что набирается руками: подключение —
// planConnect и plan.run, как connect; удаление — runProfileRm; запись
// профиля — storeProfile, как profile add и set; проверка звонков — то же,
// что hashes check. Своей логики подключения и записи профилей у меню нет.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/term"

	"github.com/FreedomCats/csqtt-cli/internal/app"
	"github.com/FreedomCats/csqtt-cli/internal/config"
	"github.com/FreedomCats/csqtt-cli/internal/supervisor"
)

func menuCommand() *command {
	return &command{
		name:    "menu",
		args:    "[флаги]",
		summary: "меню: подключение и профили вопросами, без флагов",
		description: `То же меню открывается, если запустить ` + app.Name + ` без аргументов в терминале —
например, двойным щелчком по ` + app.Name + `.exe. Команда нужна там, где запуск без
аргументов печатает список команд: ярлык, чужая оболочка.

Меню подключает к профилю, заводит профиль (по ссылке csqtt:// из панели
сервера или вопросами), меняет ссылки на звонки VK, пароль и адрес сервера,
удаляет профиль. Делает оно это теми же командами, что набираются руками.

Режиму tun нужны права root. Без них меню само запросит их для одного
подключения: на Linux — через sudo в том же терминале (sudo спросит пароль),
на Windows — окном UAC. Файл настроек и секреты при этом остаются у того,
кто открыл меню. Если прав взять негде, меню предложит подключиться в режиме
socks.

Ctrl+C во время подключения останавливает туннель и возвращает в меню,
в самом меню — закрывает его.

С -connect меню не показывается: сразу подключение к профилю, а если ссылки
на звонки перестали работать — вопрос о новых. После остановки — выход. Так
меню перезапускает себя с правами.`,
		run: runMenu,
	}
}

// wantsMenu — откроет ли run меню. Main спрашивает заранее: сигналы в меню
// разбираются по-своему (см. menuSignals), и общий обработчик ставить нельзя.
func wantsMenu(e *env, args []string) bool {
	if len(args) == 0 {
		return e.console()
	}
	return args[0] == "menu" && !wantsHelp(args[1:])
}

// console — stdin и stdout оба терминал: человек сидит перед окном. Меню
// в конвейере или из скрипта ждало бы ответов, которых не будет, поэтому
// без терминала csqtt-cli без аргументов печатает список команд.
func (e *env) console() bool {
	if e.tty != nil {
		return e.tty()
	}
	_, in := fileTerminal(e.stdin)
	_, out := fileTerminal(e.stdout)
	return in && out
}

func runMenu(ctx context.Context, e *env, args []string) error {
	cmd := menuCommand()
	fs := newFlagSet(cmd)
	configPath := addConfigFlag(fs)
	connectTo := fs.String("connect", "", "сразу подключиться к профилю, без меню; после остановки — выход")
	rest, err := parseWithArgs(e, fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("лишний аргумент: команда menu аргументов не принимает (аргумент не печатается)")
	}
	if !e.console() {
		return fmt.Errorf("меню работает только в терминале; без него — команды, список: %s help", app.Name)
	}

	m := &menu{e: e, config: *configPath, once: *connectTo != ""}
	if e.signals {
		stop := m.catchSignals()
		defer stop()
	}
	if m.once {
		return m.connectOnly(ctx, *connectTo)
	}
	return m.loop(ctx)
}

// menu — состояние одного сеанса меню.
type menu struct {
	e *env
	// config — значение -config: передаётся командам, которые зовёт меню.
	config string
	// current — профиль, выбранный пунктом «Выбрать другой профиль». Только
	// на этот сеанс: default_profile не меняется. Пусто — профиль по умолчанию.
	current string

	// once — меню без меню (-connect): одно подключение и выход.
	once bool

	// signals — куда отдать Ctrl+C; nil — сигналы не перехвачены (тесты).
	signals *menuSignals
}

// errMenuExit — человек ушёл из меню: выбрал выход, закрыл ввод или окно.
var errMenuExit = errors.New("выход из меню")

// menuItem — пункт меню. Номер — место в списке, выход — всегда 0.
type menuItem struct {
	label string
	run   func(ctx context.Context, name string) error
}

func (m *menu) loop(ctx context.Context) error {
	e := m.e
	fmt.Fprintf(e.stdout, "%s %s — клиент CSQTT: VPN поверх звонков VK\n", app.Name, e.build.Version)

	for {
		_, file, err := m.load()
		if err != nil {
			return err
		}

		name := m.selected(file)
		items := m.items(name)
		m.show(file, name, items)

		choice, err := m.choose(ctx, len(items))
		if errors.Is(err, errMenuExit) {
			// Уход из меню — решение человека, а не ошибка: паузы перед
			// закрытием окна не нужно (pause.go).
			e.noPause = true
			fmt.Fprintln(e.stdout, "До свидания.")
			return nil
		}
		if err != nil {
			return err
		}

		fmt.Fprintln(e.stdout)
		err = items[choice-1].run(ctx, name)
		switch {
		case errors.Is(err, errMenuExit):
			e.noPause = true
			return nil
		case err != nil && !errors.Is(err, context.Canceled):
			fmt.Fprintf(e.stderr, "Не получилось: %v\n", err)
		}
	}
}

// connectOnly — menu -connect: подключение с вопросами меню, но без него
// самого. Так работает перезапуск с правами (relaunchArgs): вопрос о новых
// ссылках задаёт процесс с правами. На Windows спросить больше некому — старое
// окно меню к этому времени закрыто.
func (m *menu) connectOnly(ctx context.Context, name string) error {
	err := m.connect(ctx, name)
	switch {
	case errors.Is(err, errMenuExit):
		m.e.noPause = true
		return nil
	case errors.Is(err, context.Canceled):
		// Ctrl+C до подъёма туннеля — тоже остановка, а не сбой: меню, которое
		// перезапустило этот csqtt-cli, иначе сказало бы о коде 1.
		return nil
	}
	return err
}

// load читает файл настроек. Файла нет — меню начинается с пустого списка:
// первый профиль как раз и заводится из меню.
func (m *menu) load() (string, *config.File, error) {
	return loadProfilesForWrite(m.config)
}

// selected — с каким профилем работают пункты меню: выбранный в этом сеансе,
// иначе профиль по умолчанию, иначе первый по алфавиту. Пусто — профилей нет.
func (m *menu) selected(file *config.File) string {
	if _, ok := file.Profiles[m.current]; ok {
		return m.current
	}
	m.current = ""
	if p, err := file.Profile(""); err == nil {
		return p.Name
	}
	if names := file.Names(); len(names) > 0 {
		return names[0]
	}
	return ""
}

// items — пункты меню. Номера постоянные, чтобы подсказка «пункт 1»
// оставалась верной при любом числе профилей.
func (m *menu) items(name string) []menuItem {
	if name == "" {
		return []menuItem{{label: "Создать профиль (по ссылке csqtt:// или вручную)", run: m.create}}
	}
	return []menuItem{
		{label: fmt.Sprintf("Подключиться (%s)", name), run: m.connect},
		{label: "Выбрать другой профиль", run: m.pick},
		{label: "Создать новый профиль (по ссылке csqtt:// или вручную)", run: m.create},
		{label: "Обновить ссылки на звонки VK", run: m.updateHashes},
		{label: "Заменить пароль или адрес сервера", run: m.replacePasswordOrPeer},
		{label: fmt.Sprintf("Удалить профиль %s", name), run: m.remove},
	}
}

func (m *menu) show(file *config.File, name string, items []menuItem) {
	w := m.e.stdout
	fmt.Fprintln(w)

	names := file.Names()
	if len(names) == 0 {
		fmt.Fprintln(w, "Профилей пока нет. Профиль — это адрес сервера CSQTT, пароль и ссылки на звонки VK.")
	} else {
		width, peerWidth := 0, 0
		for _, n := range names {
			width = max(width, len(n))
			peerWidth = max(peerWidth, len(config.PeerWithPort(file.Profiles[n].Peer)))
		}
		fmt.Fprintln(w, "Профили:")
		for _, n := range names {
			p := file.Profiles[n]
			marker := " "
			if n == name {
				marker = ">"
			}
			note := ""
			if isDefaultProfile(file, n) {
				note = "  (по умолчанию)"
			}
			fmt.Fprintf(w, "  %s %-*s  %-*s  %s%s\n", marker, width, n, peerWidth, config.PeerWithPort(p.Peer),
				modeText(p.Datapath), note)
		}
	}

	fmt.Fprintln(w)
	for i, item := range items {
		fmt.Fprintf(w, "  %d. %s\n", i+1, item.label)
	}
	fmt.Fprintln(w, "  0. Выход")
}

// modeText — режим профиля словами, без названий datapath.
func modeText(datapath string) string {
	if config.EffectiveDatapath(datapath) == config.DatapathTun {
		return "весь трафик"
	}
	return "только программы с прокси"
}

// choose спрашивает номер пункта, пока не получит годный. Enter — первый
// пункт, 0 и конец ввода — выход.
func (m *menu) choose(ctx context.Context, count int) (int, error) {
	for {
		line, err := m.ask(ctx, "Выберите пункт [1]: ")
		if errors.Is(err, errNoInput) {
			return 0, errMenuExit
		}
		if err != nil {
			return 0, err
		}

		n, ok := menuNumber(line, count, 1)
		switch {
		case !ok:
			fmt.Fprintf(m.e.stderr, "Нет такого пункта: %q. Введите номер от 0 до %d.\n", line, count)
		case n == 0:
			return 0, errMenuExit
		default:
			return n, nil
		}
	}
}

// menuNumber разбирает ответ на вопрос с нумерованным списком 0..count.
// Пустой ответ — fallback.
func menuNumber(line string, count, fallback int) (int, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 0 || n > count {
		return 0, false
	}
	return n, true
}

// ask задаёт вопрос и читает строку ответа. Приглашение — в stderr, как у
// остальных вопросов csqtt-cli (prompt.go).
func (m *menu) ask(ctx context.Context, prompt string) (string, error) {
	fmt.Fprint(m.e.stderr, prompt)
	line, err := m.e.readLine(ctx)
	if errors.Is(err, errNoInput) {
		fmt.Fprintln(m.e.stderr)
	}
	return line, err
}

// args — аргументы для команды, которую зовёт меню: свои плюс -config, если
// его назвали при запуске меню.
func (m *menu) args(args ...string) []string {
	if m.config != "" {
		args = append(args, "-config", m.config)
	}
	return args
}

// ──────────────────────────── пункты ────────────────────────────

func (m *menu) connect(ctx context.Context, name string) error {
	// mode — режим на этот раз; пусто — из профиля.
	mode := ""
	for {
		plan, err := m.e.planConnect(connectFlags{configPath: m.config, name: name, mode: mode, modeNamed: mode != ""})
		var refusal *tunRefusal
		if errors.As(err, &refusal) {
			if mode, err = m.tunRefused(ctx, name, refusal); err != nil || mode == "" {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}

		err = m.connectOnce(ctx, plan)
		if m.leaving() {
			return errMenuExit
		}
		var exhausted *supervisor.HashesExhaustedError
		if !errors.As(err, &exhausted) {
			return err
		}

		// Звонки закрылись — чаще всего так кончается сеанс, и выход из него
		// один: новые ссылки. Спросить их здесь же, а не отсылать в пункт 4.
		fmt.Fprintln(m.e.stderr, "Ни одна ссылка на звонок VK больше не работает: звонки закрыты или недоступны.")
		again, err := m.e.confirm(ctx, "Ввести новые ссылки и подключиться снова?")
		if err != nil || !again {
			return err
		}
		if err := m.storeHashes(ctx, name); err != nil {
			if errors.Is(err, errWizardCancelled) {
				fmt.Fprintln(m.e.stdout, "Ссылки не изменены.")
				return nil
			}
			return err
		}
	}
}

// tunRefused — профиль в режиме tun, а tun не запустится: нет прав root или
// система его не умеет. Прав не хватает — меню запрашивает их само и
// перезапускает с ними одно подключение. Если прав взять негде или система tun
// не умеет, меню объясняет почему и предлагает подключиться в socks на этот
// раз — профиль не меняется. Возвращает режим следующей попытки; пусто — не
// подключаться.
func (m *menu) tunRefused(ctx context.Context, name string, r *tunRefusal) (string, error) {
	reason := r.Error()
	if r.needsRoot && m.once {
		// menu -connect и есть перезапуск с правами — или его набрали руками.
		// Перезапускать снова нельзя: sudo, который запускает не от root
		// (обёртка в контейнере, runas_default), вкладывал бы процессы без конца.
		if os.Getenv("SUDO_USER") != "" {
			return "", fmt.Errorf("права root не получены: %s запущен через sudo, но не от root", app.Name)
		}
		return "", r
	}
	if r.needsRoot {
		// Не отсылать к sudo csqtt-cli connect, а запросить права: человек
		// из меню командной строки не знает.
		err := m.connectElevated(name)
		var no *noElevationError
		if !errors.As(err, &no) {
			return "", err
		}
		reason = no.Error()
	}
	fmt.Fprintf(m.e.stderr, "%s.\n", capitalize(reason))
	agreed, err := m.e.confirm(ctx, "Подключиться на этот раз в режиме socks — только программы, в которых указан прокси SOCKS5?")
	if err != nil || !agreed {
		return "", err
	}
	return config.DatapathSocks, nil
}

// connectOnce — один сеанс подключения. Ctrl+C на это время останавливает
// его, а не меню.
func (m *menu) connectOnce(ctx context.Context, plan *connectPlan) error {
	ctx, done := m.connecting(ctx)
	defer done()

	if m.once {
		fmt.Fprintln(m.e.stdout, "Подключение. Остановить — Ctrl+C.")
	} else {
		fmt.Fprintln(m.e.stdout, "Подключение. Остановить — Ctrl+C, после этого снова откроется меню.")
	}
	if m.e.connectRun != nil {
		return m.e.connectRun(ctx, plan)
	}
	return plan.run(ctx, m.e, false)
}

// connectElevated перезапускает подключение с правами (elevate.go) и ждёт
// его. Ошибка *noElevationError — прав взять неоткуда.
func (m *menu) connectElevated(name string) error {
	// Файл настроек — тот, что читает меню, и без -config: под sudo
	// csqtt-cli взял бы ~/.config вызвавшего, а XDG_CONFIG_HOME, из которого
	// меню могло читать, sudo не передаёт (config.Path).
	path, err := profilePath(m.config)
	if err != nil {
		return err
	}
	done := m.runningChild()
	detached, err := m.e.relaunch(relaunchArgs(name, path))
	done()

	switch {
	case err != nil:
		return err
	case detached || m.leaving():
		// Подключение ушло в своё окно с правами (Windows) — это окно
		// больше не нужно. Или, пока шло подключение, закрыли терминал.
		return errMenuExit
	}
	return nil
}

// storeHashes спрашивает новые ссылки на звонки и записывает их вместо прежних.
// Пустой ответ — отмена (errWizardCancelled): ссылки прежние, и подключения
// снова нет.
func (m *menu) storeHashes(ctx context.Context, name string) error {
	path, file, err := m.load()
	if err != nil {
		return err
	}
	p, err := file.Profile(name)
	if err != nil {
		return err
	}
	hashes, err := m.askCallLinks(ctx, linksCancel, config.MaxHashes)
	if err != nil {
		return err
	}
	return storeProfile(m.e, path, file, p, true, profileSecrets{hashes: hashes})
}

func (m *menu) pick(ctx context.Context, _ string) error {
	_, file, err := m.load()
	if err != nil {
		return err
	}
	names := file.Names()
	if len(names) < 2 {
		fmt.Fprintln(m.e.stdout, "Другого профиля нет. Завести его — пункт «Создать новый профиль».")
		return nil
	}

	for i, n := range names {
		fmt.Fprintf(m.e.stdout, "  %d. %s  %s\n", i+1, n, config.PeerWithPort(file.Profiles[n].Peer))
	}
	fmt.Fprintln(m.e.stdout, "  0. Назад")

	for {
		line, err := m.ask(ctx, "Какой профиль? [0]: ")
		if errors.Is(err, errNoInput) {
			return nil
		}
		if err != nil {
			return err
		}
		n, ok := menuNumber(line, len(names), 0)
		if !ok {
			fmt.Fprintf(m.e.stderr, "Нет такого профиля: %q. Введите номер от 0 до %d.\n", line, len(names))
			continue
		}
		if n > 0 {
			m.current = names[n-1]
		}
		return nil
	}
}

func (m *menu) remove(ctx context.Context, name string) error {
	return runProfileRm(ctx, m.e, m.args(name))
}

// ──────────────────────────── сигналы ────────────────────────────

// menuSignals разбирает Ctrl+C, SIGTERM (так Windows сообщает о закрытии
// окна) и SIGHUP (закрыли терминал) всё время, пока открыто меню.
//
// Ctrl+C во время подключения или проверки звонков отменяет только их:
// туннель снимается, и человек возвращается в меню. SIGTERM и SIGHUP тоже
// сначала дают снять туннель, но потом меню закрывается: возвращаться
// некуда. Вне подключения любой из них закрывает меню сразу. Второй сигнал
// во время остановки — выход сразу, как у csqtt-cli connect: ждать зависшую
// уборку человек не обязан.
type menuSignals struct {
	mu sync.Mutex
	// cancel — отменить идущее подключение; nil — подключения нет.
	cancel context.CancelFunc
	// closing — во время подключения пришёл SIGTERM или SIGHUP: после
	// остановки туннеля меню закрывается.
	closing bool
	// child — идёт подключение, перезапущенное с правами (sudo): сигнал
	// терминал отдал и ему, и разбирается с ним он. Меню ждёт его конца.
	child bool
	// exit — выйти, не дожидаясь меню.
	exit func()
}

// catchSignals ставит обработчик на всё время меню и возвращает функцию,
// которая его снимает.
func (m *menu) catchSignals() func() {
	// Сигнал может прийти посреди скрытого ввода, когда эхо в терминале
	// выключено. Выход без восстановления оставил бы терминал немым.
	var restore func()
	if fd, ok := m.e.terminalFD(); ok {
		if state, err := term.GetState(fd); err == nil {
			restore = func() { _ = term.Restore(fd, state) }
		}
	}

	s := &menuSignals{exit: func() {
		if restore != nil {
			restore()
		}
		fmt.Fprintln(m.e.stderr)
		os.Exit(exitInterrupted)
	}}
	m.signals = s

	// Тот же набор, что у connect (supervisor.Interrupt): под nohup SIGHUP
	// игнорируется, и подписываться на него нельзя — подписка его вернула бы.
	stopOn := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if !signal.Ignored(syscall.SIGHUP) {
		stopOn = append(stopOn, syscall.SIGHUP)
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, stopOn...)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-ch:
				s.handle(sig)
			case <-done:
				return
			}
		}
	}()

	return func() {
		signal.Stop(ch)
		close(done)
		m.signals = nil
	}
}

func (s *menuSignals) handle(sig os.Signal) {
	s.mu.Lock()
	if s.child {
		// Ctrl+C остановит подключение в запущенном csqtt-cli, и меню
		// вернётся. Закрытый терминал — то же, но возвращаться некуда.
		if sig != os.Interrupt {
			s.closing = true
		}
		s.mu.Unlock()
		return
	}
	cancel := s.cancel
	s.cancel = nil
	if cancel != nil && sig != os.Interrupt {
		s.closing = true
	}
	s.mu.Unlock()

	if cancel != nil {
		cancel()
		return
	}
	s.exit()
}

// connecting заводит контекст подключения, который отменит Ctrl+C. Вторая
// функция — подключение кончилось, Ctrl+C снова закрывает меню.
func (m *menu) connecting(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	s := m.signals
	if s == nil {
		return ctx, cancel
	}

	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	return ctx, func() {
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
		cancel()
	}
}

// runningChild — запущен csqtt-cli с правами: пока он идёт, сигналы меню не
// трогают. Возвращает функцию, которая снимает отметку.
func (m *menu) runningChild() func() {
	s := m.signals
	if s == nil {
		return func() {}
	}
	s.mu.Lock()
	s.child = true
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.child = false
		s.mu.Unlock()
	}
}

// leaving — во время подключения закрыли окно или терминал: меню больше не
// показывается.
func (m *menu) leaving() bool {
	s := m.signals
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing
}

// exitInterrupted — код выхода по Ctrl+C: 128 + SIGINT, как принято в оболочках.
const exitInterrupted = 130
