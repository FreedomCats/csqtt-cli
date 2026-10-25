package supervisor

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
)

// routingLine — событие ROUTING, как его печатает ядро: правило — строкой
// JSON внутри JSON. Домены и адреса выдуманы.
func routingLine(t *testing.T, rule string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"routing": rule})
	if err != nil {
		t.Fatal(err)
	}
	return "__CSQTT_EVENT__|ROUTING|" + string(payload)
}

const (
	ruleOffice = `{"version":2,"name":"Офис","domains_is_whitelist":false,"domains":["example.ru"],` +
		`"template_domains":false,"ip_is_whitelist":false,"ips":["203.0.113.0/24"]}`
	ruleTemplate = `{"version":2,"name":"Шаблон","template_domains":true}`
)

// update — команда, которую оболочка шлёт ядру на правило.
func update(t *testing.T, rule string) string {
	t.Helper()
	e, ok := coreproc.ParseLine(routingLine(t, rule)).(coreproc.RoutingEvent)
	if !ok || e.Rule == nil {
		t.Fatalf("правило %s не разобрано", rule)
	}
	u, err := e.Rule.Update()
	if err != nil {
		t.Fatal(err)
	}
	return u.Command
}

// sendWatch — ядро, которое запоминает, на каком сообщении ему прислали
// команду: последнее отданное циклу сообщение и есть то, на которое цикл
// ответил.
type sendWatch struct {
	Core
	h  *harness
	on *[]coreproc.Message
}

func (s sendWatch) Send(cmd string) error {
	s.h.mu.Lock()
	if n := len(s.h.msgs); n > 0 {
		*s.on = append(*s.on, s.h.msgs[n-1])
	}
	s.h.mu.Unlock()
	return s.Core.Send(cmd)
}

// С согласием правило уходит ядру командой UPDATE_DOMAINS после PROCESS, один
// раз на процесс ядра: повтор того же правила тому же ядру не шлётся, а новое
// ядро (следующая попытка) получает его снова. Правило, пришедшее раньше
// PROCESS, ждёт его.
func TestServerRoutingApplied(t *testing.T) {
	early := routingLine(t, ruleOffice)
	h := newHarness(t, Options{Config: coreproc.Config{ServerRouting: true}},
		join(recStart, []string{routingLine(t, ruleOffice), "wait UPDATE_DOMAINS", routingLine(t, ruleOffice), "exit 1"}),
		join([]string{early}, recStart, []string{"wait UPDATE_DOMAINS", routingLine(t, ruleOffice), "exit 1"}))
	var on []coreproc.Message
	h.tunnel.start = func(cfg coreproc.Config) (Core, error) {
		c, err := h.start(cfg)
		return sendWatch{Core: c, h: h, on: &on}, err
	}
	if err := h.result(h.run()); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	cores := h.attempts()
	if len(cores) < 2 {
		t.Fatalf("попыток %d", len(cores))
	}
	want := []string{update(t, ruleOffice)}
	for i, c := range cores[:2] {
		if got := c.commands(); !slices.Equal(got, want) {
			t.Errorf("ядру %d послано %q, ждали %q", i+1, got, want)
		}
	}
	if len(on) != 2 {
		t.Fatalf("команды посланы на %v", on)
	}
	if _, ok := on[0].(coreproc.RoutingEvent); !ok {
		t.Errorf("первому ядру команда послана не на ROUTING, а на %#v", on[0])
	}
	if _, ok := on[1].(coreproc.PIDEvent); !ok {
		t.Errorf("правило до PROCESS послано не на PROCESS, а на %#v", on[1])
	}
}

// Без согласия правило только показывается: ядру не шлётся ничего.
func TestServerRoutingWithoutConsent(t *testing.T) {
	h := newHarness(t, Options{Config: coreproc.Config{TemplateDomains: true}},
		join(recConnect, []string{routingLine(t, ruleOffice), "exit 1"}))
	if err := h.result(h.run()); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if got := h.attempts()[0].commands(); len(got) != 0 {
		t.Errorf("ядру послано %q", got)
	}
}

// Правило, совпадающее с тем, с чем ядро запущено, не шлётся. Правило, которое
// не разобрать, после применённого возвращает ядру настройки профиля; ещё одно
// такое — уже ничего.
func TestServerRoutingRevertsToProfile(t *testing.T) {
	h := newHarness(t, Options{Config: coreproc.Config{ServerRouting: true, TemplateDomains: true}},
		join(recConnect, []string{
			routingLine(t, ruleTemplate),
			routingLine(t, ruleOffice),
			"wait UPDATE_DOMAINS",
			routingLine(t, "не json"),
			"wait UPDATE_DOMAINS",
			routingLine(t, "[1]"),
			routingLine(t, ruleTemplate),
			"exit 1",
		}))
	if err := h.result(h.run()); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	want := []string{update(t, ruleOffice), coreproc.ProfileRouting(true).Command}
	if got := h.attempts()[0].commands(); !slices.Equal(got, want) {
		t.Errorf("ядру послано\n%q\nждали\n%q", got, want)
	}
}

// Правило, пускающее трафик мимо туннеля, — как маршрутизация из профиля:
// подозрение на сеть снимает только READY, не рост принятого. Правило без
// прямого трафика этого не меняет.
func TestServerRoutingDirectNeedsReady(t *testing.T) {
	for _, tc := range []struct {
		rule   string
		direct bool
	}{
		{ruleOffice, true},
		{`{"version":2,"name":"Пусто","domains_is_whitelist":true}`, false},
	} {
		script := join(recConnect, []string{routingLine(t, tc.rule)})
		if tc.direct {
			script = append(script, "wait UPDATE_DOMAINS")
		}
		script = append(script,
			`__CSQTT_EVENT__|STATS|{"active":2,"bytes_down":500,"bytes_up":100}`,
			`__CSQTT_EVENT__|NETWORK_SUSPECT|{}`,
			`__CSQTT_EVENT__|STATS|{"active":2,"bytes_down":9000,"bytes_up":900}`,
			`__CSQTT_EVENT__|READY|{"worker":5}`,
			"wait never")
		h := newHarness(t, Options{DegradedRestart: 3600e9, Config: coreproc.Config{ServerRouting: true}}, script)
		done := h.run()
		h.waitState(StateConnected)
		h.waitState(StateDegraded)
		h.waitState(StateConnected)
		h.cancel()
		if err := h.result(done); err != nil {
			t.Fatalf("Run() = %v", err)
		}
		got := h.triggered(StateConnected)
		want := []coreproc.Message{coreproc.ReadyEvent{Worker: 1}, coreproc.StatsEvent{Active: 2, BytesUp: 900, BytesDown: 9000}}
		if tc.direct {
			want[1] = coreproc.ReadyEvent{Worker: 5}
		}
		if !slices.Equal(got, want) {
			t.Errorf("правило %s: в «работает» вели %v, ожидались %v", tc.rule, got, want)
		}
	}
}
