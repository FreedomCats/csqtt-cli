package cli

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/supervisor"
)

// fixedClock — часы принтера в тестах: время в строках предсказуемо, аптайм
// двигается вручную.
type fixedClock struct{ t time.Time }

func newClock() *fixedClock {
	return &fixedClock{t: time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)}
}

func (c *fixedClock) now() time.Time { return c.t }

func (c *fixedClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func testPrinter(verbose bool) (*printer, *bytes.Buffer, *fixedClock) {
	var buf bytes.Buffer
	clock := newClock()
	p := newPrinter(&buf, verbose)
	p.now = clock.now
	return p, &buf, clock
}

func move(p *printer, from, to supervisor.State) {
	p.transition(supervisor.Transition{From: from, To: to})
}

// connect проводит принтер по успешной линии до «туннель работает».
func connect(p *printer) {
	move(p, supervisor.StateIdle, supervisor.StateStarting)
	move(p, supervisor.StateStarting, supervisor.StateCredentials)
	move(p, supervisor.StateCredentials, supervisor.StateConfiguring)
	move(p, supervisor.StateConfiguring, supervisor.StateConnected)
}

func TestRedirectedOutputIsPlainLines(t *testing.T) {
	p, buf, clock := testPrinter(false)
	p.header(18)
	p.setConnectedNote("SOCKS5 на 127.0.0.1:1080")
	connect(p)
	p.message(coreproc.StatsEvent{Active: 18, BytesUp: 2048, BytesDown: 3 * 1024 * 1024})
	clock.advance(5 * time.Second)
	p.refresh()
	p.finish()
	p.line("остановлено")

	got := buf.String()
	for _, forbidden := range []string{"\r", "\x1b"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("в перенаправленном выводе есть %q:\n%s", forbidden, got)
		}
	}
	want := []string{
		"12:00:00 == запуск ядра\n",
		"12:00:00 == получены креды VK\n",
		"12:00:00 == поднимается туннель\n",
		"12:00:00 == туннель работает · SOCKS5 на 127.0.0.1:1080\n",
		"12:00:05 остановлено\n",
	}
	if got != strings.Join(want, "") {
		t.Fatalf("вывод:\n%s\nожидалось:\n%s", got, strings.Join(want, ""))
	}
}

// Без -v журнал ядра, SOCKS5 и мелкие события молчат; с -v — печатаются.
func TestVerboseOnlyDetails(t *testing.T) {
	quiet := []coreproc.Message{
		coreproc.Log{Text: "[TURN] Allocate"},
		coreproc.PIDEvent{PID: 42},
		coreproc.ReadyEvent{Worker: 3},
		coreproc.ProgressEvent{Kind: "credentials"},
		coreproc.ErrorEvent{Code: "turn", Message: "таймаут"},
		coreproc.CaptchaRequest{Mode: "auto"},
		coreproc.StoppedEvent{},
		coreproc.ConfigEvent{Address: netip.MustParseAddr("192.0.2.2"), DNS: []netip.Addr{netip.MustParseAddr("198.51.100.53")}},
		coreproc.StatsEvent{Active: 9, BytesUp: 10, BytesDown: 20},
		coreproc.WorkersEvent{Limit: 0},
		coreproc.TunLostEvent{},
		coreproc.DTLSEvent{Worker: 2, Message: "DTLS-хендшейк успешен (ECDSA P-256)"},
	}
	p, buf, _ := testPrinter(false)
	for _, m := range quiet {
		p.message(m)
	}
	p.debugf("[SOCKS] CONNECT example.com:443: отказ")
	if buf.Len() != 0 {
		t.Fatalf("без -v напечатано:\n%s", buf.String())
	}

	p, buf, _ = testPrinter(true)
	for _, m := range quiet {
		p.message(m)
	}
	p.debugf("[SOCKS] CONNECT example.com:443: отказ")
	for _, want := range []string{
		"ядро: [TURN] Allocate\n",
		"ядро запущено, pid 42\n",
		"воркер 3 на линии\n",
		"ошибка turn: таймаут\n",
		"капчу, режим auto\n",
		"192.0.2.2, DNS 198.51.100.53\n",
		"воркеров на линии 9, отправлено 10 Б, получено 20 Б\n",
		"[SOCKS] CONNECT example.com:443: отказ\n",
		"ядро: у сервера нет лимита воркеров для этого пароля\n",
		"ядро: потерян дескриптор адаптера (TUN_LOST)\n",
		"ядро: DTLS, воркер 2: DTLS-хендшейк успешен (ECDSA P-256)\n",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("с -v нет строки %q:\n%s", want, buf.String())
		}
	}
}

// Лимит воркеров сервера меньше запрошенного — строка и без -v, одна на
// значение: повтор того же лимита молчит, новый — новая строка. Лимит не
// меньше запрошенного и «лимита нет» (0) молчат; с -v лимит виден всегда.
func TestWorkersLimitLine(t *testing.T) {
	const line = "сервер ограничил число воркеров для этого пароля"
	p, buf, _ := testPrinter(false)
	p.header(30)
	for _, limit := range []int{0, 30, 50, 20, 20, 0, 10, 10} {
		p.message(coreproc.WorkersEvent{Limit: limit})
	}
	want := []string{
		"12:00:00 " + line + ": 20 вместо 30\n",
		"12:00:00 " + line + ": 10 вместо 30\n",
	}
	if got := buf.String(); got != strings.Join(want, "") {
		t.Errorf("вывод:\n%s\nожидалось:\n%s", got, strings.Join(want, ""))
	}

	p, buf, _ = testPrinter(true)
	p.header(30)
	p.message(coreproc.WorkersEvent{Limit: 20})
	p.message(coreproc.WorkersEvent{Limit: 20})
	if got := strings.Count(buf.String(), "ядро: лимит воркеров у сервера — 20\n"); got != 2 {
		t.Errorf("с -v строк журнала о лимите %d, ждали 2:\n%s", got, buf.String())
	}
	if got := strings.Count(buf.String(), line); got != 1 {
		t.Errorf("с -v строк об ограничении %d, ждали 1:\n%s", got, buf.String())
	}
}

// Правило маршрутизации от сервера — строка и без -v, одна на правило: сервер
// шлёт его на каждый запуск ядра, и повтор молчит, а другое правило — новая
// строка. Неразборчивое — тоже строка, с текстом как есть, обрезанным.
func TestRoutingLine(t *testing.T) {
	office := coreproc.RoutingEvent{Raw: `{"name":"Офис"}`, Rule: &coreproc.RoutingRule{
		Name:            "Офис",
		Domains:         []string{"example.ru", "shop.example.com"},
		TemplateDomains: true,
		IPsWhitelist:    true,
		IPs:             []string{"203.0.113.0/24"},
		Packages:        []string{"com.example.app"},
	}}
	empty := coreproc.RoutingEvent{Raw: `{}`, Rule: &coreproc.RoutingRule{DomainsWhitelist: true}}
	broken := coreproc.RoutingEvent{Raw: "не json\n" + strings.Repeat("x", 300)}
	// U+202E развернул бы остаток строки, U+2066 и U+200B — невидимы.
	bidi := coreproc.RoutingEvent{Raw: "bidi", Rule: &coreproc.RoutingRule{Name: "Оф\u202eис\u2066\u200b"}}

	p, buf, _ := testPrinter(false)
	for _, e := range []coreproc.RoutingEvent{office, office, empty, broken, broken, bidi} {
		p.message(e)
	}
	got := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	want := []string{
		"12:00:00 сервер прислал правило маршрутизации «Офис»: домены — 2 (чёрный список), шаблон — да, " +
			"адреса — 1 (белый список) — не применяется",
		"12:00:00 сервер прислал правило маршрутизации без имени: домены — 0, шаблон — нет, адреса — 0 — не применяется",
	}
	if len(got) != 4 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("вывод:\n%s\nожидалось начало:\n%s", buf.String(), strings.Join(want, "\n"))
	}
	if b := got[2]; !strings.Contains(b, "разобрать его не удалось (не json xxx") ||
		!strings.Contains(b, "…) — не применяется") || len([]rune(b)) > 250 {
		t.Errorf("строка о неразборчивом правиле: %q", b)
	}
	if b := got[3]; !strings.Contains(b, "правило маршрутизации «Офис»: ") {
		t.Errorf("знаки форматирования в имени не убраны: %q", b)
	}

	// С -v — ещё и списки, длинные — обрезаны; повтор — строкой журнала.
	p, buf, _ = testPrinter(true)
	many := make([]string, 25)
	for i := range many {
		many[i] = fmt.Sprintf("d%d.example.ru", i)
	}
	long := coreproc.RoutingEvent{Raw: "long", Rule: &coreproc.RoutingRule{Name: "Длинное", Domains: many}}
	p.message(office)
	p.message(office)
	p.message(long)
	for _, want := range []string{
		"  домены, чёрный список: example.ru, shop.example.com\n",
		"  адреса, белый список: 203.0.113.0/24\n",
		"  приложения Android (на компьютере неприменимо), чёрный список: com.example.app\n",
		"ядро: сервер снова прислал то же правило маршрутизации\n",
		"правило маршрутизации «Длинное»: домены — 25 (чёрный список)",
		"  домены, чёрный список: d0.example.ru, d1.example.ru, d2.example.ru, d3.example.ru, d4.example.ru, " +
			"d5.example.ru, d6.example.ru, d7.example.ru, d8.example.ru, d9.example.ru и ещё 15\n",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("с -v нет строки %q:\n%s", want, buf.String())
		}
	}
	if n := strings.Count(buf.String(), "«Офис»"); n != 1 {
		t.Errorf("строк о правиле «Офис» с -v %d, ждали 1", n)
	}
}

// Сервер без DNS — предупреждение и без -v: имена через SOCKS5 не пройдут.
func TestConfigWithoutDNSWarns(t *testing.T) {
	p, buf, _ := testPrinter(false)
	p.message(coreproc.ConfigEvent{Address: netip.MustParseAddr("192.0.2.2")})
	if !strings.Contains(buf.String(), "не выдал DNS") {
		t.Errorf("нет предупреждения:\n%s", buf.String())
	}
}

// Хеши называются номерами в профиле: ни в строках, ни в журнале ядра их нет.
func TestHashesShownByNumber(t *testing.T) {
	p, buf, _ := testPrinter(true)
	p.setHashes([]string{testHashA, testHashB})
	p.message(coreproc.CallUnavailableEvent{Hash: testHashB, Code: 954})
	p.message(coreproc.CallUnavailableEvent{Hash: testHashB, Code: 954}) // повтор — молча
	p.message(coreproc.Log{Text: "[VKCalls] " + testHashA + " и " + testHashB})
	p.logf("цикл: %s", testHashA)
	p.transition(supervisor.Transition{From: supervisor.StateConnected, To: supervisor.StateReconnecting,
		Cause: errors.New("ошибка про " + testHashB)})

	got := buf.String()
	noSecrets(t, "вывод", got)
	for _, want := range []string{
		"хеш №2 профиля: звонок VK недоступен (ошибка VK 954)\n",
		"ядро: [VKCalls] <хеш №1> и <хеш №2>\n",
		"цикл: <хеш №1>\n",
		"ошибка про <хеш №2>\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет строки %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "недоступен") != 1 {
		t.Errorf("повтор CALL_UNAVAILABLE напечатан:\n%s", got)
	}
	if d := p.deadHashes(); len(d) != 1 || d[0] != 2 {
		t.Errorf("deadHashes = %v", d)
	}
}

// Ошибка HTTP-клиента с адресом шага 5 гостевого пути в режиме с хешами:
// хеш назван номером, временные ключи спрятаны ещё в coreproc.
func TestGuestKeysHiddenInLog(t *testing.T) {
	const anonToken, sessionKey = "$fakeOKtoken.0123-abcd", "fake-sess.key_0123"
	p, buf, _ := testPrinter(true)
	p.setHashes([]string{testHashA, testHashB})
	p.message(coreproc.ParseLine("[КРЕД #1] Ошибка: error sending request for url (https://calls.okcdn.ru/fb.do?joinLink=" +
		testHashA + "&isVideo=false&protocolVersion=5&anonymToken=" + anonToken +
		"&method=vchat.joinConversationByLink&format=JSON&application_key=CGMMEJLGDIHBABABA&session_key=" + sessionKey +
		"): operation timed out. Повторяем через 1.2s..."))
	got := buf.String()
	noSecrets(t, "вывод", got)
	want := "ядро: [КРЕД #1] Ошибка: error sending request for url (https://calls.okcdn.ru/fb.do?joinLink=<хеш №1>" +
		"&isVideo=false&protocolVersion=5&anonymToken=<скрыто>&method=vchat.joinConversationByLink&format=JSON" +
		"&application_key=CGMMEJLGDIHBABABA&session_key=<скрыто>): operation timed out. Повторяем через 1.2s...\n"
	if !strings.Contains(got, want) || strings.Contains(got, anonToken) || strings.Contains(got, sessionKey) {
		t.Errorf("вывод:\n%s\nожидалась строка:\n%s", got, want)
	}
}

// Номер недоступного хеша уходит в строку цикла, которая идёт следом, а не
// отдельной строкой; не пришла — печатается сам.
func TestDeadHashJoinsLoopLine(t *testing.T) {
	p, buf, _ := testPrinter(false)
	p.setHashes([]string{testHashA, testHashB})
	p.message(coreproc.CallUnavailableEvent{Hash: testHashA, Code: 954})
	p.logf("звонок VK недоступен (ошибка VK 954) — хеш исключён, годных осталось 1")
	p.message(coreproc.CallUnavailableEvent{Hash: testHashB, Code: 9008})
	p.finish()
	want := "12:00:00 хеш №1 профиля: звонок VK недоступен (ошибка VK 954) — хеш исключён, годных осталось 1\n" +
		"12:00:00 хеш №2 профиля: звонок VK недоступен (ошибка VK 9008)\n"
	if got := buf.String(); got != want {
		t.Errorf("вывод:\n%s\nожидалось:\n%s", got, want)
	}
}

// Хеш, который начинается с другого, заменяется целиком.
func TestHashMaskLongestFirst(t *testing.T) {
	m := hashMask([]string{"abcdefgh", "abcdefghijkl", "short"})
	if got := m.Replace("abcdefghijkl abcdefgh short"); got != "<хеш №2> <хеш №1> short" {
		t.Errorf("маска: %q", got)
	}
}

// Начало хеша из журнала ядра («хеш: <8 знаков>...») тоже заменяется номером;
// общее у двух хешей начало — без номера.
func TestHashMaskStarts(t *testing.T) {
	m := hashMask([]string{testHashA, testHashB, "sameheadXXXXXXXXXXXXXXXX", "sameheadYYYYYYYYYYYYYYYY"})
	for in, want := range map[string]string{
		"[КРЕД #1] Запрос (хеш: " + testHashA[:8] + "...)": "[КРЕД #1] Запрос (хеш: <хеш №1>...)",
		"[КРЕД #2] Запрос (хеш: " + testHashB[:8] + "...)": "[КРЕД #2] Запрос (хеш: <хеш №2>...)",
		"[КРЕД #3] Запрос (хеш: samehead...)":              "[КРЕД #3] Запрос (хеш: <хеш>...)",
		"полный " + testHashB + "...":                      "полный <хеш №2>...",
		"полный sameheadYYYYYYYYYYYYYYYY":                  "полный <хеш №4>",
	} {
		if got := m.Replace(in); got != want {
			t.Errorf("маска %q: %q, ожидалось %q", in, got, want)
		}
	}

	p, buf, _ := testPrinter(true)
	p.setHashes([]string{testHashA, testHashB})
	p.message(coreproc.Log{Text: "[КРЕД #1] Запрос (хеш: " + testHashA[:8] + "...)"})
	if got := buf.String(); strings.Contains(got, testHashA[:8]) || !strings.Contains(got, "(хеш: <хеш №1>...)") {
		t.Errorf("начало хеша в журнале:\n%s", got)
	}
}

func TestReconnectAndDegradedLines(t *testing.T) {
	p, buf, _ := testPrinter(false)
	connect(p)
	p.transition(supervisor.Transition{From: supervisor.StateConnected, To: supervisor.StateDegraded,
		Cause: supervisor.ErrNoWorkers})
	p.transition(supervisor.Transition{From: supervisor.StateDegraded, To: supervisor.StateReconnecting,
		Cause: errors.New("ядро завершилось само"), Delay: 2 * time.Second})
	buf2 := buf.String()
	for _, want := range []string{
		"== туннель без связи: живых воркеров нет\n",
		"== переподключение через 2с: ядро завершилось само\n",
	} {
		if !strings.Contains(buf2, want) {
			t.Errorf("нет строки %q:\n%s", want, buf2)
		}
	}

	// Начало повторной попытки — не строка.
	buf.Reset()
	move(p, supervisor.StateReconnecting, supervisor.StateStarting)
	if buf.Len() != 0 {
		t.Errorf("начало второй попытки напечатано строкой: %q", buf.String())
	}
}

func TestStatusModelRender(t *testing.T) {
	clock := newClock()
	var m statusModel
	if got := m.render(clock.now()); got != "" {
		t.Fatalf("пустая модель дала %q", got)
	}

	m.workersTotal, m.hashesTotal = 18, 3
	m.moved(supervisor.Transition{From: supervisor.StateIdle, To: supervisor.StateStarting}, clock.now())
	clock.advance(3 * time.Second)
	if got, want := m.render(clock.now()), "запуск ядра · 3с"; got != want {
		t.Errorf("первая попытка: %q, ожидалось %q", got, want)
	}

	m.moved(supervisor.Transition{From: supervisor.StateStarting, To: supervisor.StateConnected}, clock.now())
	m.stats(coreproc.StatsEvent{Active: 18, BytesUp: 1024, BytesDown: 1024 * 1024}, clock.now())
	clock.advance(3 * time.Second)
	m.stats(coreproc.StatsEvent{Active: 18, BytesUp: 4096, BytesDown: 4 * 1024 * 1024}, clock.now())
	clock.advance(56 * time.Second)
	m.dead = map[int]bool{2: true}
	if got, want := m.render(clock.now()),
		"туннель работает · 59с · воркеров 18/18 · ↑ 4.0 КиБ (1.0 КиБ/с) ↓ 4.0 МиБ (1.0 МиБ/с) · хешей 2/3"; got != want {
		t.Errorf("подключено: %q, ожидалось %q", got, want)
	}

	// Воркеры — из автомата, если он подключён.
	m.workersNow = func() int { return 9 }
	if got := m.render(clock.now()); !strings.Contains(got, "воркеров 9/18") {
		t.Errorf("воркеры из автомата: %q", got)
	}
	m.workersNow = nil

	// «Без связи» и обратно аптайм не сбрасывают.
	m.moved(supervisor.Transition{From: supervisor.StateConnected, To: supervisor.StateDegraded}, clock.now())
	clock.advance(30 * time.Second)
	if got := m.render(clock.now()); !strings.HasPrefix(got, "туннель без связи · 1м 29с") {
		t.Errorf("деградация: %q", got)
	}
	m.moved(supervisor.Transition{From: supervisor.StateDegraded, To: supervisor.StateConnected}, clock.now())
	if got := m.render(clock.now()); !strings.HasPrefix(got, "туннель работает · 1м 29с") {
		t.Errorf("восстановление: %q", got)
	}

	// Новая попытка: ядро считает трафик заново, сеанс — нет; скорость
	// обнуляется.
	m.moved(supervisor.Transition{From: supervisor.StateConnected, To: supervisor.StateReconnecting}, clock.now())
	if got, want := m.render(clock.now()), "переподключение · 0с · хешей 2/3"; got != want {
		t.Errorf("переподключение: %q, ожидалось %q", got, want)
	}
	m.moved(supervisor.Transition{From: supervisor.StateReconnecting, To: supervisor.StateStarting}, clock.now())
	if got, want := m.render(clock.now()), "запуск ядра · 0с · попытка 2 · хешей 2/3"; got != want {
		t.Errorf("вторая попытка: %q, ожидалось %q", got, want)
	}
	m.moved(supervisor.Transition{From: supervisor.StateStarting, To: supervisor.StateConnected}, clock.now())
	m.stats(coreproc.StatsEvent{Active: 9, BytesUp: 1024}, clock.now())
	if got := m.render(clock.now()); !strings.Contains(got, "↑ 5.0 КиБ ↓ 4.0 МиБ ·") {
		t.Errorf("трафик сеанса: %q", got)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0с"},
		{-time.Second, "0с"},
		{7 * time.Second, "7с"},
		{65 * time.Second, "1м 05с"},
		{time.Hour + 2*time.Minute + 15*time.Second, "1ч 02м 15с"},
		{49*time.Hour + 30*time.Minute, "2д 01ч"},
	}
	for _, c := range cases {
		if got := formatDuration(c.d); got != c.want {
			t.Errorf("formatDuration(%v) = %q, ожидалось %q", c.d, got, c.want)
		}
	}
}

func TestFormatBytesAndRate(t *testing.T) {
	cases := map[int64]string{0: "0 Б", 1023: "1023 Б", 1536: "1.5 КиБ", 5 << 20: "5.0 МиБ", 3 << 30: "3.0 ГиБ"}
	for n, want := range cases {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, ожидалось %q", n, got, want)
		}
	}
	if got := formatRate(0.5); got != "" {
		t.Errorf("нулевая скорость напечатана: %q", got)
	}
	if got := formatRate(2048); got != " (2.0 КиБ/с)" {
		t.Errorf("formatRate = %q", got)
	}
}

func TestStatusLineRedrawsUnderLines(t *testing.T) {
	p, buf, clock := testPrinter(false)
	p.enableStatus(func() int { return 80 })
	p.header(18)

	move(p, supervisor.StateIdle, supervisor.StateStarting)
	if got, want := buf.String(), "12:00:00 == запуск ядра\n\rзапуск ядра · 0с"; got != want {
		t.Fatalf("после первой строки:\n%q\nожидалось:\n%q", got, want)
	}
	shown := len([]rune("запуск ядра · 0с"))

	// Следующая строка стирает статус пробелами, печатается и рисует статус
	// заново под собой.
	buf.Reset()
	clock.advance(time.Second)
	p.line("строка")
	want := "\r" + strings.Repeat(" ", shown) + "\r" + "12:00:01 строка\n" + "\rзапуск ядра · 1с"
	if got := buf.String(); got != want {
		t.Fatalf("после второй строки:\n%q\nожидалось:\n%q", got, want)
	}

	// Перерисовка без новой строки: та же длина — только возврат каретки.
	buf.Reset()
	clock.advance(time.Second)
	p.refresh()
	if got, want := buf.String(), "\rзапуск ядра · 2с"; got != want {
		t.Fatalf("перерисовка:\n%q\nожидалось:\n%q", got, want)
	}

	// Строка стала короче — хвост прежней стирается, курсор возвращается.
	p.model.state = supervisor.StateConfiguring
	p.refresh()
	text := "поднимается туннель · 2с"
	buf.Reset()
	p.model.state = supervisor.StateStopping
	p.refresh()
	shorter := "остановка · 2с"
	diff := len([]rune(text)) - len([]rune(shorter))
	if got, want := buf.String(), "\r"+shorter+strings.Repeat(" ", diff)+"\r"+shorter; got != want {
		t.Fatalf("укорочение:\n%q\nожидалось:\n%q", got, want)
	}

	// finish убирает строку, дальше идут только строки.
	buf.Reset()
	p.finish()
	p.line("остановлено")
	if got, want := buf.String(), "\r"+strings.Repeat(" ", len([]rune(shorter)))+"\r"+"12:00:02 остановлено\n"; got != want {
		t.Fatalf("после finish:\n%q\nожидалось:\n%q", got, want)
	}
	p.finish() // повторный вызов безвреден
}

func TestStatusLineTruncatesToWidth(t *testing.T) {
	p, buf, _ := testPrinter(false)
	p.enableStatus(func() int { return 10 })
	move(p, supervisor.StateIdle, supervisor.StateStarting)
	if got, want := buf.String(), "12:00:00 == запуск ядра\n\rзапуск яд"; got != want {
		t.Fatalf("обрезка:\n%q\nожидалось:\n%q", got, want)
	}

	buf.Reset()
	p.enableStatus(func() int { return 0 })
	p.refresh()
	if got := buf.String(); len([]rune(got)) > defaultStatusWidth {
		t.Fatalf("без ширины строка длиннее %d: %q", defaultStatusWidth, got)
	}
}

// STATS без -v — только перерисовка строки состояния, не строка.
func TestStatusLineStatsRefreshQuietly(t *testing.T) {
	p, buf, _ := testPrinter(false)
	p.enableStatus(func() int { return 120 })
	p.header(18)
	connect(p)

	buf.Reset()
	p.message(coreproc.StatsEvent{Active: 18, BytesUp: 1024, BytesDown: 2048})
	got := buf.String()
	if strings.Contains(got, "\n") {
		t.Fatalf("счётчики без -v напечатаны строкой:\n%q", got)
	}
	if !strings.Contains(got, "воркеров 18/18 · ↑ 1.0 КиБ ↓ 2.0 КиБ") {
		t.Fatalf("строка состояния без счётчиков:\n%q", got)
	}
}

func TestAsideWriterPausesStatusLine(t *testing.T) {
	p, buf, _ := testPrinter(false)
	p.enableStatus(func() int { return 80 })
	move(p, supervisor.StateIdle, supervisor.StateStarting)
	shown := len([]rune("запуск ядра · 0с"))

	var side bytes.Buffer
	buf.Reset()
	if _, err := p.aside(&side).Write([]byte("Вставьте токен: ")); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "\r"+strings.Repeat(" ", shown)+"\r"; got != want {
		t.Fatalf("перед приглашением статус не стёрт:\n%q\nожидалось:\n%q", got, want)
	}
	if side.String() != "Вставьте токен: " {
		t.Fatalf("приглашение не дошло: %q", side.String())
	}

	// Пока человек отвечает, ни тикер, ни STATS строку не рисуют.
	buf.Reset()
	p.refresh()
	p.message(coreproc.StatsEvent{Active: 1})
	if buf.Len() != 0 {
		t.Fatalf("статус нарисован поверх приглашения: %q", buf.String())
	}

	// Следующая строка принтера возвращает статус.
	buf.Reset()
	p.line("капча: ответ отправлен")
	if got, want := buf.String(), "12:00:00 капча: ответ отправлен\n\rзапуск ядра · 0с"; got != want {
		t.Fatalf("после строки:\n%q\nожидалось:\n%q", got, want)
	}
}
