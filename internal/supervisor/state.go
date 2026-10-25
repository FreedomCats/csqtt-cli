// Package supervisor сводит ядро и datapath в один туннель: держит состояние
// подключения, решает, когда перезапускать ядро, и останавливает его
// (doc/architecture.md §09).
//
// Три части: автомат (этот файл), политика перезапуска — задержки и годность
// хешей (policy.go) — и цикл подключения (lifecycle.go), где живут процесс
// ядра, таймеры и горутины.
//
// state.go — сам автомат. Он ничего не запускает и не ждёт: принимает события
// ядра и решения цикла, а отдаёт состояние и переходы. Таймеров внутри нет
// намеренно: автомат ведётся событиями, а время живёт в lifecycle.go.
package supervisor

import (
	"errors"
	"sync"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
)

// State — состояние туннеля. Набор повторяет схему из architecture.md §09.
type State string

const (
	// StateIdle — ничего не запущено: до первой попытки подключения.
	StateIdle State = "idle"
	// StateStarting — ядро запущено и идёт в VK за кредами TURN.
	StateStarting State = "starting"
	// StateCredentials — VK выдал креды TURN (PROGRESS credentials); дальше —
	// TURN и рукопожатие с сервером, о которых ядро событий не шлёт.
	StateCredentials State = "credentials"
	// StateConfiguring — сервер выдал адрес туннеля (TUNCONF), поднимается
	// datapath.
	StateConfiguring State = "configuring"
	// StateConnected — datapath поднят, и хотя бы один воркер на линии.
	StateConnected State = "connected"
	// StateDegraded — туннель был поднят, но живых воркеров нет или ядро
	// заподозрило сеть (ACTIVE_ZERO, NETWORK_SUSPECT). Воркеров ядро
	// переподключает само; затянется — цикл перезапустит ядро. Подозрение на
	// сеть снимает новый воркер или, без маршрутизации мимо туннеля, рост
	// принятого трафика.
	StateDegraded State = "degraded"
	// StateReconnecting — попытка кончилась, ядро вышло, следующая попытка
	// ждёт своей задержки.
	StateReconnecting State = "reconnecting"
	// StateStopping — останавливаемся окончательно: по Ctrl+C, по отказу
	// сервера или потому, что годных хешей не осталось. Состояние конечное.
	StateStopping State = "stopping"
)

// lineRank — место состояния на линии подключения. Линия проходится только
// вперёд: ядро шлёт PROGRESS на каждый полученный кред, и поздний кред одной
// группы не должен возвращать поднятый туннель в «получены креды».
//
// Состояний вне линии — Degraded, Reconnecting, Stopping — в карте нет:
// отсутствие ранга и означает «события линии сюда не применяются».
var lineRank = map[State]int{
	StateIdle:        0,
	StateStarting:    1,
	StateCredentials: 2,
	StateConfiguring: 3,
	StateConnected:   4,
}

// stateLabels — названия состояний для вывода.
var stateLabels = map[State]string{
	StateIdle:         "не подключено",
	StateStarting:     "запуск ядра",
	StateCredentials:  "получены креды VK",
	StateConfiguring:  "поднимается туннель",
	StateConnected:    "туннель работает",
	StateDegraded:     "туннель без связи",
	StateReconnecting: "переподключение",
	StateStopping:     "остановка",
}

// Label — название состояния по-русски. У неизвестного состояния — его же код:
// печатать пустую строку вместо состояния хуже, чем печатать «idle».
func (s State) Label() string {
	if label, ok := stateLabels[s]; ok {
		return label
	}
	return string(s)
}

// Причины ухода в StateDegraded.
var (
	ErrNoWorkers      = errors.New("живых воркеров нет")
	ErrNetworkSuspect = errors.New("серия таймаутов TURN — похоже, пропала сеть")
)

// Transition — состоявшийся переход. Возвращается каждым методом, меняющим
// состояние, чтобы цикл напечатал строку и не восстанавливал причину перехода
// из состояния постфактум.
type Transition struct {
	From State
	To   State
	// Cause — почему: у перехода в StateDegraded — ErrNoWorkers или
	// ErrNetworkSuspect, у перехода в StateReconnecting и StateStopping —
	// чем кончилась попытка. nil — причины нет (остановили по просьбе).
	Cause error
	// Delay — через сколько начнётся следующая попытка; только у перехода в
	// StateReconnecting.
	Delay time.Duration
}

// Machine — конечный автомат подключения.
//
// Ведёт его цикл из одной горутины, но State читают и другие — строка
// состояния перерисовывается по таймеру, — поэтому всё под замком.
type Machine struct {
	mu    sync.Mutex
	state State

	// direct — ядро пускает часть трафика мимо туннеля
	// (coreproc.Config.DirectRouting), и принятое напрямую STATS считает
	// вместе с принятым из туннеля. Задаёт NewTunnel, на весь Run.
	direct bool
	// directRule — то же по правилу сервера, посланному ядру этой попытки
	// (RuleDirect); BeginAttempt снимает: новое ядро начинает с настроек
	// профиля.
	directRule bool

	// Что известно о текущей попытке; BeginAttempt обнуляет.
	datapathUp bool  // datapath поднят по первому TUNCONF
	workers    int   // живых воркеров: READY прибавляет, STATS и ACTIVE_ZERO уточняют
	suspect    bool  // NETWORK_SUSPECT, и с тех пор ни нового воркера, ни трафика
	bytesDown  int64 // принято ядром, по последнему STATS; при direct — и напрямую
	suspectAt  int64 // bytesDown на момент NETWORK_SUSPECT
	draining   bool  // ядру послан Stop: попытка доживает (Drain)
}

// New возвращает автомат в состоянии StateIdle.
func New() *Machine {
	return &Machine{state: StateIdle}
}

// State — текущее состояние.
func (m *Machine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Workers — сколько воркеров на линии, по последним событиям ядра.
func (m *Machine) Workers() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workers
}

// BeginAttempt открывает попытку подключения: StateIdle или StateReconnecting
// переходят в StateStarting, сведения о прошлой попытке забываются.
//
// Из остальных состояний — ничего: попытка уже идёт, а из StateStopping
// переходов нет вовсе.
func (m *Machine) BeginAttempt() (Transition, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state != StateIdle && m.state != StateReconnecting {
		return Transition{}, false
	}
	m.datapathUp, m.workers, m.suspect, m.bytesDown, m.suspectAt = false, 0, false, 0, 0
	m.draining, m.directRule = false, false
	return m.set(StateStarting, nil)
}

// RuleDirect отмечает, что ядру этой попытки послано правило сервера,
// пускающее часть трафика мимо туннеля: подозрение на сеть с этих пор, как
// при direct, снимает только READY. До конца попытки, даже если следующее
// правило маршрутизацию выключит: ответы уже открытых прямых соединений
// могут ещё идти.
func (m *Machine) RuleDirect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.directRule = true
}

// Drain отмечает, что ядру послан Stop и попытка доживает: ядро закрывает
// сессии, и воркеры уходят по нашей же просьбе. С этого момента события ядра
// ещё уточняют счётчики, но состояния не меняют — иначе остановка выглядела бы
// как «туннель без связи». Снимает отметку следующий BeginAttempt.
func (m *Machine) Drain() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.draining = true
}

// Observe применяет сообщение ядра. Сообщения, которые на состояние не влияют
// (журнал, капча, ошибки), автомат пропускает: их разбирает цикл.
//
// Вне попытки — в StateIdle, StateReconnecting и StateStopping — сообщение
// отбрасывается целиком: ядро прошлой попытки уже не должно перерисовывать
// картину. После Drain сообщение учитывается, но перехода не даёт.
func (m *Machine) Observe(msg coreproc.Message) (Transition, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch m.state {
	case StateIdle, StateReconnecting, StateStopping:
		return Transition{}, false
	}

	switch e := msg.(type) {
	case coreproc.ProgressEvent:
		if e.Kind == "credentials" && !m.draining {
			return m.advance(StateCredentials)
		}
	case coreproc.ConfigEvent:
		if !m.draining {
			return m.advance(StateConfiguring)
		}
	case coreproc.ReadyEvent:
		// Новый воркер дошёл до сервера — значит, и сеть есть.
		m.workers++
		m.suspect = false
	case coreproc.StatsEvent:
		m.workers = e.Active
		m.bytesDown = e.BytesDown
		// Трафик после подозрения — знак, что сеть вернулась. Но не при
		// direct: ответы прямых соединений ядро считает туда же, и они
		// идут, когда туннель мёртв. Тогда подозрение снимает только
		// READY — воркеры, упавшие по таймаутам, ядро переподключает само.
		if m.suspect && !m.direct && !m.directRule && e.Active > 0 && e.BytesDown > m.suspectAt {
			m.suspect = false
		}
	case coreproc.ActiveZeroEvent:
		m.workers = 0
	case coreproc.NetworkSuspectEvent:
		m.suspect = true
		m.suspectAt = m.bytesDown
	default:
		return Transition{}, false
	}
	if m.draining {
		return Transition{}, false
	}
	return m.health()
}

// DatapathUp отмечает, что datapath поднят по первому TUNCONF попытки. Туннель
// становится рабочим, как только на линии есть воркер: READY может прийти и
// раньше TUNCONF — их шлют разные задачи ядра.
func (m *Machine) DatapathUp() (Transition, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state != StateConfiguring {
		return Transition{}, false
	}
	m.datapathUp = true
	if m.draining {
		return Transition{}, false
	}
	return m.health()
}

// Reconnect уводит автомат с линии: попытка кончилась, ядро вышло, следующая
// попытка начнётся через delay. Из StateIdle, StateReconnecting и
// StateStopping — ничего.
func (m *Machine) Reconnect(cause error, delay time.Duration) (Transition, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch m.state {
	case StateIdle, StateReconnecting, StateStopping:
		return Transition{}, false
	}
	tr, ok := m.set(StateReconnecting, cause)
	tr.Delay = delay
	return tr, ok
}

// Stop переводит автомат в конечное StateStopping. Работает из любого
// состояния, включая StateIdle: Ctrl+C до первой попытки — тоже остановка.
func (m *Machine) Stop(cause error) (Transition, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.set(StateStopping, cause)
}

// advance двигает состояние вперёд по линии; назад и с линии — никогда.
// Вызывается при захваченном m.mu.
func (m *Machine) advance(to State) (Transition, bool) {
	rank, onLine := lineRank[m.state]
	if !onLine || lineRank[to] <= rank {
		return Transition{}, false
	}
	return m.set(to, nil)
}

// health переводит поднятый туннель между StateConnected и StateDegraded.
// Вызывается при захваченном m.mu.
func (m *Machine) health() (Transition, bool) {
	if !m.datapathUp {
		return Transition{}, false
	}
	switch m.state {
	case StateConfiguring, StateDegraded:
		if m.workers > 0 && !m.suspect {
			return m.set(StateConnected, nil)
		}
	case StateConnected:
		switch {
		case m.workers == 0:
			return m.set(StateDegraded, ErrNoWorkers)
		case m.suspect:
			return m.set(StateDegraded, ErrNetworkSuspect)
		}
	}
	return Transition{}, false
}

// set меняет состояние и собирает переход. Вызывается при захваченном m.mu.
// Переход в то же состояние переходом не считается: печатать «работает →
// работает» незачем.
func (m *Machine) set(to State, cause error) (Transition, bool) {
	if m.state == to {
		return Transition{}, false
	}
	tr := Transition{From: m.state, To: to, Cause: cause}
	m.state = to
	return tr, true
}
