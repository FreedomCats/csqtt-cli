// output.go — вывод хода подключения: события ядра и смена состояния
// строками, строка состояния со счётчиками и аптаймом для терминала.
//
// Два приёма вывода, и они не мешают друг другу. Строки — одна на значимое
// событие, с временем, без перерисовки: это то, что уходит в перенаправленный
// вывод, и в файле после `csqtt-cli connect > log.txt` нет ничего, кроме них.
// Строка состояния появляется только когда stdout — терминал: она живёт под
// последней строкой и перерисовывается возвратом каретки и пробелами.
// Escape-последовательностей нет и там: `\r` понимает любая консоль, включая
// старый conhost, а стирать строку пробелами ничем не хуже, чем командой
// терминала.
//
// Без -v печатаются переходы автомата, исключённые хеши, строки цикла и
// предупреждения; журнал ядра, SOCKS5 и капчи, счётчики трафика строками и
// мелкие события ядра — только с -v.
package cli

import (
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/supervisor"
)

// defaultStatusWidth — ширина терминала, если её не удалось узнать.
const defaultStatusWidth = 80

// minMaskedHash — хеши короче этого журнал не прячет: короткий «хеш» нашёлся
// бы и в обычных словах журнала. Настоящий хеш звонка VK — десятки знаков.
const minMaskedHash = 8

// hashStartRunes — сколько первых знаков хеша ядро печатает в журнале
// запроса кредов (worker.rs, «[КРЕД #N] Запрос (хеш: <начало>...)»).
const hashStartRunes = 8

// printer печатает события и переходы. Пишет только в свой Writer: в
// `csqtt-cli connect` это stdout команды, и тесты подставляют туда буфер.
//
// Свой замок нужен, хотя супервизор и сериализует OnMessage, OnTransition и
// Logf: через тот же принтер пишут мост капчи (из своей горутины), SOCKS5 (из
// своих) и тикер строки состояния, а строки не должны перемешиваться.
type printer struct {
	w       io.Writer
	verbose bool
	now     func() time.Time

	mu sync.Mutex

	// model — то, из чего собирается строка состояния. Ведётся всегда,
	// печатается только при status != nil: разделять сбор и показ дешевле,
	// чем спрашивать на каждом событии, есть ли терминал.
	model statusModel
	// status — строка состояния. nil означает, что stdout не терминал и
	// строки нет.
	status *statusLine

	// hashes — хеши профиля по порядку: строки называют хеш его номером в
	// профиле, а журнал ядра печатается с номерами вместо хешей. Хеш — ключ к
	// звонку, ему не место ни на экране, ни в перенаправленном выводе.
	hashes []string
	mask   *strings.Replacer
	// connectedNote дописывается к строке «туннель работает»: куда теперь
	// направлять программы.
	connectedNote string
	// pendingDead — хеш, о недоступности которого ядро только что сообщило:
	// цикл следом пишет строку «хеш исключён…», и номер хеша идёт в неё же, а
	// не отдельной строкой. Пришла другая строка раньше — номер печатается
	// сам (flushDead). Ноль — ждать нечего.
	pendingDead     int
	pendingDeadCode int64
	// workersLimit — лимит воркеров сервера из последнего WORKERS: строка
	// о нём печатается, только когда он сменился.
	workersLimit int
}

func newPrinter(w io.Writer, verbose bool) *printer {
	return &printer{w: w, verbose: verbose, now: time.Now, mask: strings.NewReplacer()}
}

// statusLine — состояние строки состояния на экране.
type statusLine struct {
	// width отдаёт ширину терминала в знаках. Спрашивается на каждую
	// перерисовку: окно могут растянуть по ходу работы. Ноль и меньше —
	// ширина неизвестна, берётся defaultStatusWidth.
	width func() int
	// shown — сколько знаков сейчас занимает строка на экране. Ноль —
	// строки на экране нет.
	shown int
	// paused — кто-то пишет в терминал мимо принтера и ждёт ответа
	// пользователя (ручной режим капчи). Перерисовывать строку в это время
	// нельзя: она встала бы посреди приглашения. Снимается следующей
	// строкой принтера.
	paused bool
}

// enableStatus включает строку состояния. Зовётся один раз до запуска,
// когда известно, что stdout — терминал.
func (p *printer) enableStatus(width func() int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = &statusLine{width: width}
}

// setHashes запоминает хеши профиля: по ним строки называют хеш номером, и их
// же журнал прячет.
func (p *printer) setHashes(hashes []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hashes = slices.Clone(hashes)
	p.mask = hashMask(hashes)
	p.model.hashesTotal = len(hashes)
}

// hashMask — замена хешей на их номера в профиле: «<хеш №2>». Длинные хеши
// идут первыми, чтобы хеш, начинающийся с другого, не заменился наполовину.
//
// Ядро печатает и начало хеша — первые восемь знаков с многоточием
// («[КРЕД #1] Запрос (хеш: <начало>...)»), поэтому заменяется и оно:
// «<хеш №2>...». Если начало у двух хешей профиля общее, номер по нему не
// узнать, и замена — без номера: «<хеш>...». Настоящие хеши — десятки
// знаков: они длиннее начал и остаются впереди.
func hashMask(hashes []string) *strings.Replacer {
	type pair struct{ old, new string }
	var list []pair
	var starts []string
	owners := map[string][]int{}
	for i, h := range hashes {
		if len(h) < minMaskedHash {
			continue
		}
		list = append(list, pair{h, "<хеш №" + strconv.Itoa(i+1) + ">"})
		start := truncateRunes(h, hashStartRunes) + "..."
		if _, seen := owners[start]; !seen {
			starts = append(starts, start)
		}
		owners[start] = append(owners[start], i+1)
	}
	for _, start := range starts {
		mark := "<хеш>..."
		if n := owners[start]; len(n) == 1 {
			mark = "<хеш №" + strconv.Itoa(n[0]) + ">..."
		}
		list = append(list, pair{start, mark})
	}
	slices.SortStableFunc(list, func(a, b pair) int { return len(b.old) - len(a.old) })
	pairs := make([]string, 0, 2*len(list))
	for _, p := range list {
		pairs = append(pairs, p.old, p.new)
	}
	return strings.NewReplacer(pairs...)
}

// hashNumber — номер хеша в профиле, с единицы; 0 — такого в профиле нет.
func hashNumber(hashes []string, hash string) int {
	return slices.Index(hashes, hash) + 1
}

// header запоминает, сколько воркеров ядро запустит: строка состояния
// показывает «18/18», а событие ядра приносит только первую цифру.
func (p *printer) header(workersTotal int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.model.workersTotal = workersTotal
}

// setWorkers — откуда брать число воркеров на линии: автомат супервизора
// (Tunnel.Workers) считает их по READY, STATS и ACTIVE_ZERO и потому точнее
// одного STATS раз в три секунды.
func (p *printer) setWorkers(workers func() int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.model.workersNow = workers
}

// setConnectedNote — что дописать к строке «туннель работает».
func (p *printer) setConnectedNote(note string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connectedNote = note
}

// line печатает одну строку с временем. Формат без даты: аптайм измеряется
// часами, и дата видна из соседних строк, если вывод перенаправлен.
//
// Строка состояния на время печати убирается и рисуется заново под новой
// строкой — так она всегда остаётся последней на экране.
func (p *printer) line(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lineLocked(fmt.Sprintf(format, args...))
}

func (p *printer) lineLocked(text string) {
	p.flushDead()
	p.writeLine(text)
}

// flushDead печатает номер недоступного хеша отдельной строкой, если строка
// цикла за ним не пришла. Под замком.
func (p *printer) flushDead() {
	if n := p.pendingDead; n != 0 {
		p.pendingDead = 0
		p.writeLine(fmt.Sprintf("хеш №%d профиля: звонок VK недоступен (ошибка VK %d)", n, p.pendingDeadCode))
	}
}

func (p *printer) writeLine(text string) {
	p.clearStatus()
	if p.status != nil {
		p.status.paused = false
	}
	fmt.Fprintf(p.w, "%s %s\n", p.now().Format("15:04:05"), text)
	p.drawStatus()
}

// logf — строки, которые печатаются всегда: цикл подключения (хеш исключён,
// DNS не сменился). Хеши в них прячутся, как и в журнале.
func (p *printer) logf(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	text := p.mask.Replace(fmt.Sprintf(format, args...))
	if n := p.pendingDead; n != 0 {
		p.pendingDead = 0
		text = fmt.Sprintf("хеш №%d профиля: %s", n, text)
	}
	p.lineLocked(text)
}

// debugf — подробности, только с -v: журнал SOCKS5 (в том числе отказы
// CONNECT с целью) и журнал моста капчи.
func (p *printer) debugf(format string, args ...any) {
	if !p.verbose {
		return
	}
	p.logf(format, args...)
}

// refresh перерисовывает строку состояния — для тикера: аптайм идёт, даже
// когда событий нет.
func (p *printer) refresh() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status == nil || p.status.paused {
		return
	}
	p.drawStatus()
}

// refreshEvery заводит тикер перерисовки и возвращает функцию его снятия.
// Без терминала тикер не нужен, и функция снятия пустая.
func (p *printer) refreshEvery(every time.Duration) func() {
	p.mu.Lock()
	enabled := p.status != nil
	p.mu.Unlock()
	if !enabled {
		return func() {}
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.refresh()
			case <-stop:
				return
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

// finish убирает строку состояния насовсем: дальше идут только строки.
// Повторный вызов безвреден.
func (p *printer) finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearStatus()
	p.status = nil
	p.flushDead()
}

// aside возвращает Writer для того, кто пишет в терминал мимо принтера и
// ждёт ответа пользователя, — для моста капчи, чьё приглашение ручного режима
// уходит в stderr. Перед записью строка состояния убирается и не рисуется,
// пока принтер не напечатает следующую строку: иначе она встала бы прямо в
// приглашении, где человек вводит токен.
func (p *printer) aside(w io.Writer) io.Writer {
	return asideWriter{p: p, w: w}
}

type asideWriter struct {
	p *printer
	w io.Writer
}

func (a asideWriter) Write(b []byte) (int, error) {
	a.p.mu.Lock()
	defer a.p.mu.Unlock()
	a.p.clearStatus()
	if a.p.status != nil {
		a.p.status.paused = true
	}
	return a.w.Write(b)
}

// clearStatus стирает строку состояния с экрана. Зовётся под замком.
func (p *printer) clearStatus() {
	if p.status == nil || p.status.shown == 0 {
		return
	}
	fmt.Fprintf(p.w, "\r%s\r", strings.Repeat(" ", p.status.shown))
	p.status.shown = 0
}

// drawStatus рисует строку состояния поверх прежней. Зовётся под замком.
//
// Строка обрезается по ширине терминала за вычетом одного знака: строка во
// всю ширину переводит курсор на следующую строку, и `\r` уже не вернёт его
// назад.
func (p *printer) drawStatus() {
	if p.status == nil || p.status.paused {
		return
	}
	text := p.model.render(p.now())
	width := p.status.width()
	if width <= 0 {
		width = defaultStatusWidth
	}
	text = truncateRunes(text, width-1)

	// Новая строка может быть короче прежней: хвост прежней стирается
	// пробелами, курсор возвращается к её началу и остаётся там же, где
	// стоял бы после чистой перерисовки — в конце текста.
	n := utf8.RuneCountInString(text)
	pad := ""
	if p.status.shown > n {
		pad = strings.Repeat(" ", p.status.shown-n) + "\r" + text
	}
	fmt.Fprintf(p.w, "\r%s%s", text, pad)
	p.status.shown = n
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// message печатает сообщение ядра (supervisor.Options.OnMessage).
func (p *printer) message(m coreproc.Message) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch m := m.(type) {
	case coreproc.Log:
		if p.verbose {
			p.lineLocked("ядро: " + p.mask.Replace(m.Text))
		}

	case coreproc.PIDEvent:
		if p.verbose {
			p.lineLocked(fmt.Sprintf("ядро запущено, pid %d", m.PID))
		}

	case coreproc.ConfigEvent:
		if len(m.DNS) == 0 {
			p.lineLocked("предупреждение: сервер не выдал DNS — имена через SOCKS5 не разрешатся, " +
				"годятся только адреса")
		} else if p.verbose {
			p.lineLocked(fmt.Sprintf("сервер выдал адрес туннеля %v, DNS %v", m.Address, joinAddrs(m.DNS)))
		}

	case coreproc.ProgressEvent:
		if p.verbose {
			p.lineLocked("ядро: шаг " + m.Kind)
		}

	case coreproc.ReadyEvent:
		if p.verbose {
			p.lineLocked(fmt.Sprintf("воркер %d на линии", m.Worker))
		}

	case coreproc.StatsEvent:
		p.model.stats(m, p.now())
		if p.verbose {
			p.lineLocked(fmt.Sprintf("воркеров на линии %d, отправлено %s, получено %s",
				m.Active, formatBytes(m.BytesUp), formatBytes(m.BytesDown)))
		} else if p.status != nil && !p.status.paused {
			p.drawStatus()
		}

	case coreproc.CallUnavailableEvent:
		n := hashNumber(p.hashes, m.Hash)
		if n == 0 || p.model.dead[n] {
			return
		}
		if p.model.dead == nil {
			p.model.dead = map[int]bool{}
		}
		p.model.dead[n] = true
		// Строку об исключении хеша цикл напишет следом (logf) — номер уйдёт
		// в неё.
		p.flushDead()
		p.pendingDead, p.pendingDeadCode = n, m.Code

	case coreproc.WorkersEvent:
		p.workersEvent(m)

	case coreproc.TunLostEvent:
		// Что с этим делать, решает цикл — и пишет строку сам (только с
		// дескриптором адаптера, tun на Linux).
		if p.verbose {
			p.lineLocked("ядро: потерян дескриптор адаптера (TUN_LOST)")
		}

	case coreproc.ErrorEvent:
		// Фатальную ошибку назовёт переход: попытка с ней кончается.
		if !m.Fatal && p.verbose {
			p.lineLocked(fmt.Sprintf("ядро: ошибка %s: %s", m.Code, p.mask.Replace(m.Message)))
		}

	case coreproc.CaptchaRequest:
		// Окно и приглашение показывает мост капчи; здесь — только для журнала.
		if p.verbose {
			p.lineLocked("ядро просит решить капчу, режим " + m.Mode)
		}

	case coreproc.StoppedEvent:
		if p.verbose {
			p.lineLocked("ядро остановилось")
		}
	}
}

// workersEvent — сервер ответил на согласование числа воркеров. Лимит, меньший
// запрошенного ядром, виден и без -v — одной строкой, пока он не сменится:
// воркеров на линии будет меньше, чем обещала строка профиля. Строка
// состояния по-прежнему считает от запрошенного. Под замком.
func (p *printer) workersEvent(e coreproc.WorkersEvent) {
	if p.verbose {
		if e.Limit == 0 {
			p.lineLocked("ядро: у сервера нет лимита воркеров для этого пароля")
		} else {
			p.lineLocked(fmt.Sprintf("ядро: лимит воркеров у сервера — %d", e.Limit))
		}
	}
	if e.Limit == p.workersLimit {
		return
	}
	p.workersLimit = e.Limit
	if asked := p.model.workersTotal; e.Limit > 0 && e.Limit < asked {
		p.lineLocked(fmt.Sprintf("сервер ограничил число воркеров для этого пароля: %d вместо %d", e.Limit, asked))
	}
}

// transition печатает смену состояния (supervisor.Options.OnTransition).
func (p *printer) transition(tr supervisor.Transition) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.model.moved(tr, p.now())

	text := "== " + tr.To.Label()
	switch tr.To {
	case supervisor.StateStarting:
		// Начало повторной попытки видно по строке «переподключение»; строка
		// о запуске ядра здесь только шумит. Первой попытке нужно откуда-то
		// начаться.
		if tr.From != supervisor.StateIdle {
			p.drawStatus()
			return
		}
	case supervisor.StateConnected:
		if p.connectedNote != "" {
			text += " · " + p.connectedNote
		}
	case supervisor.StateReconnecting:
		if tr.Delay > 0 {
			text += " через " + formatDuration(tr.Delay)
		}
		if tr.Cause != nil {
			text += ": " + p.mask.Replace(tr.Cause.Error())
		}
	case supervisor.StateDegraded:
		if tr.Cause != nil {
			text += ": " + tr.Cause.Error()
		}
	case supervisor.StateStopping:
		// Почему остановлено, напечатает команда: ошибка уходит в stderr
		// и решает код возврата.
	}
	p.lineLocked(text)
}

// deadHashes — номера хешей профиля, звонки которых оказались недоступны, по
// возрастанию.
func (p *printer) deadHashes() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	var dead []int
	for n := range p.model.dead {
		dead = append(dead, n)
	}
	slices.Sort(dead)
	return dead
}

func joinAddrs(addrs []netip.Addr) string {
	s := make([]string, len(addrs))
	for i, a := range addrs {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}

// ──────────────────────── строка состояния ────────────────────────

// statusModel — то, что показывает строка состояния. Собирается из тех же
// событий и переходов, что идут в строки: у принтера они уже есть, и
// спрашивать супервизор отдельно незачем — кроме числа воркеров, которое
// точнее знает его автомат.
type statusModel struct {
	state supervisor.State
	// since — когда вошли в текущее состояние.
	since time.Time
	// connectedAt — когда туннель поднялся; нулевое время — не поднят.
	// Переход в «туннель без связи» и обратно аптайм не сбрасывает.
	connectedAt time.Time
	// attempt — номер текущей попытки с начала работы.
	attempt int

	// workersNow — сколько воркеров на линии (Tunnel.Workers); nil — по
	// последнему STATS.
	workersNow   func() int
	workers      int
	workersTotal int

	// up и down — счётчики текущей попытки: ядро считает с запуска, а
	// запуск у каждой попытки свой, поэтому прошлые попытки копятся в
	// upBefore и downBefore.
	up, down             int64
	upBefore, downBefore int64
	// statsAt — когда пришёл последний STATS; rateUp и rateDown — скорость
	// между двумя последними, байт в секунду.
	statsAt          time.Time
	rateUp, rateDown float64

	hashesTotal int
	// dead — номера хешей профиля, исключённых как недоступные.
	dead map[int]bool
}

func (m *statusModel) moved(tr supervisor.Transition, now time.Time) {
	m.state = tr.To
	m.since = now
	switch tr.To {
	case supervisor.StateStarting:
		m.attempt++
		m.connectedAt = time.Time{}
		m.workers = 0
		m.upBefore += m.up
		m.downBefore += m.down
		m.up, m.down = 0, 0
		m.statsAt = time.Time{}
		m.rateUp, m.rateDown = 0, 0
	case supervisor.StateConnected:
		if tr.From != supervisor.StateDegraded {
			m.connectedAt = now
		}
	case supervisor.StateDegraded:
		// Аптайм продолжает идти: туннель поднят, только молчит.
	default:
		m.connectedAt = time.Time{}
	}
}

// stats учитывает STATS: воркеров, счётчики и скорость с прошлого STATS.
func (m *statusModel) stats(e coreproc.StatsEvent, now time.Time) {
	if !m.statsAt.IsZero() {
		if dt := now.Sub(m.statsAt).Seconds(); dt > 0 {
			m.rateUp = rate(e.BytesUp-m.up, dt)
			m.rateDown = rate(e.BytesDown-m.down, dt)
		}
	}
	m.statsAt = now
	m.workers = e.Active
	m.up, m.down = e.BytesUp, e.BytesDown
}

func rate(delta int64, seconds float64) float64 {
	if delta < 0 {
		return 0
	}
	return float64(delta) / seconds
}

// render собирает строку: состояние, время в нём, и дальше то, что в этом
// состоянии есть смысл показывать. Поля разделяются точкой посередине.
func (m *statusModel) render(now time.Time) string {
	if m.state == "" {
		return ""
	}
	parts := []string{m.state.Label()}

	switch m.state {
	case supervisor.StateConnected, supervisor.StateDegraded:
		if !m.connectedAt.IsZero() {
			parts = append(parts, formatDuration(now.Sub(m.connectedAt)))
		}
		workers := m.workers
		if m.workersNow != nil {
			workers = m.workersNow()
		}
		if m.workersTotal > 0 {
			parts = append(parts, fmt.Sprintf("воркеров %d/%d", workers, m.workersTotal))
		} else {
			parts = append(parts, fmt.Sprintf("воркеров %d", workers))
		}
		parts = append(parts, fmt.Sprintf("↑ %s%s ↓ %s%s",
			formatBytes(m.upBefore+m.up), formatRate(m.rateUp),
			formatBytes(m.downBefore+m.down), formatRate(m.rateDown)))
	default:
		if !m.since.IsZero() {
			parts = append(parts, formatDuration(now.Sub(m.since)))
		}
		if m.attempt > 1 {
			parts = append(parts, fmt.Sprintf("попытка %d", m.attempt))
		}
	}

	if len(m.dead) > 0 && m.hashesTotal > 0 {
		parts = append(parts, fmt.Sprintf("хешей %d/%d", m.hashesTotal-len(m.dead), m.hashesTotal))
	}
	return strings.Join(parts, " · ")
}

// formatRate — скорость в скобках после счётчика; нулевая не печатается.
func formatRate(bytesPerSec float64) string {
	if bytesPerSec < 1 {
		return ""
	}
	return " (" + formatBytes(int64(bytesPerSec)) + "/с)"
}

// formatDuration — длительность в виде «5с», «3м 05с», «1ч 02м 15с», «2д 03ч».
// Точность падает с ростом: за сутки секунды никого не интересуют.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	h := int(d / time.Hour)
	d -= time.Duration(h) * time.Hour
	m := int(d / time.Minute)
	s := int((d - time.Duration(m)*time.Minute) / time.Second)

	switch {
	case days > 0:
		return fmt.Sprintf("%dд %02dч", days, h)
	case h > 0:
		return fmt.Sprintf("%dч %02dм %02dс", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dм %02dс", m, s)
	default:
		return fmt.Sprintf("%dс", s)
	}
}

// formatBytes — объём в удобных единицах. Точности в один знак достаточно:
// счётчик читают, чтобы понять, идёт ли трафик вообще.
func formatBytes(n int64) string {
	const unit = 1024
	switch {
	case n >= unit*unit*unit:
		return fmt.Sprintf("%.1f ГиБ", float64(n)/(unit*unit*unit))
	case n >= unit*unit:
		return fmt.Sprintf("%.1f МиБ", float64(n)/(unit*unit))
	case n >= unit:
		return fmt.Sprintf("%.1f КиБ", float64(n)/unit)
	default:
		return fmt.Sprintf("%d Б", n)
	}
}
