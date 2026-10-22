// proxy.go — локальный прокси для окна капчи при kill-switch.
//
// Сеть окна WebView2 делает msedgewebview2.exe, а не csqtt-cli, и при kill-switch
// («закрыто») она закрыта. Разрешать его по процессу нельзя: тем же бинарём
// пользуются Teams, Outlook, виджеты, и они пошли бы мимо туннеля. Поэтому
// окно ходит через этот прокси, а он — сокетами csqtt-cli, которые kill-switch
// пропускает: диалер с хуком netguard и резолвер ядра.
//
// Прокси слушает loopback, и до него может достучаться любой процесс машины.
// Чтобы он не стал дырой в kill-switch, пускает он только CONNECT на порт 443
// доменов VK (proxyDomains) и живёт, пока открыто окно.
//
// Тот же прокси с другими правилами — у окна входа VK в диагностическом
// режиме (netfilter.go): там kill-switch нет, а правила и журнал задаёт
// пользователь.
package captcha

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// DialFunc — чем прокси соединяется с сайтом: DialContext у net.Dialer.
type DialFunc = func(ctx context.Context, network, address string) (net.Conn, error)

// ErrProxy — прокси для окна не поднялся, окно не открывалось. Bridge по нему
// открывает то же окно без прокси, под Options.Direct: kill-switch на время
// капчи снимается.
var ErrProxy = errors.New("прокси для окна капчи не поднялся")

// proxyDomains — куда окну капчи можно ходить: сам домен и его поддомены.
// Страница — id.vk.com (id.vk.ru), статика — st.vk.com (st.vk.ru), её API —
// api.vk.com, login.vk.com; картинки и звук капчи — CDN VK. Реклама и
// сборщики отпечатков, которые страница тоже зовёт (ad.mail.ru,
// openfpcdn.io), капче не нужны — им 403.
var proxyDomains = []string{
	"vk.com",
	"vk.ru",
	"vk.me",
	"userapi.com",
	"vkuser.net",
	"vkuseraudio.net",
	"mycdn.me",
	"vk-cdn.net",
}

// proxyPort — единственный порт, куда пускает прокси: страница капчи вся
// на HTTPS.
const proxyPort = "443"

const (
	// proxyHeaderTimeout — сколько ждать строки CONNECT от клиента.
	proxyHeaderTimeout = 10 * time.Second
	// proxyDialTimeout — сколько ждать соединения с сайтом.
	proxyDialTimeout = 15 * time.Second
)

// proxyAllowed — можно ли окну капчи к host. Адреса вместо имён не
// пускаются: страница VK ходит по именам, а адрес проверить не по чему.
func proxyAllowed(host string) bool {
	return domainAllowed(host, proxyDomains)
}

// domainAllowed — host — один из domains или его поддомен. Адрес вместо
// имени — нет.
func domainAllowed(host string, domains []string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	for _, domain := range domains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// proxyPolicy — кого пускает прокси и что пишет о соединениях.
type proxyPolicy interface {
	// allow решает, пускать ли запрос к target («хост:порт»; пусто — адрес
	// не разобрать): CONNECT или, если plain, обычный HTTP-запрос method.
	// Отказ — код ответа клиенту, 0 — пускать. О решении политика пишет
	// сама.
	allow(target string, plain bool, method string) int
	// failed — пропущенное соединение с target не установилось.
	failed(target string, plain bool, err error)
}

// captchaPolicy — правила окна капчи: только CONNECT на 443 к доменам VK,
// в журнал — только отказы.
type captchaPolicy struct {
	logf func(string, ...any)
}

func (c captchaPolicy) allow(target string, plain bool, method string) int {
	if plain {
		c.logf("[CAPTCHA] прокси: отказ %s %s — только CONNECT", method, target)
		return http.StatusMethodNotAllowed
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil || port != proxyPort || !proxyAllowed(host) {
		c.logf("[CAPTCHA] прокси: отказ %s — не домен VK на порту 443", target)
		return http.StatusForbidden
	}
	return 0
}

func (c captchaPolicy) failed(target string, _ bool, err error) {
	c.logf("[CAPTCHA] прокси: %s не соединился: %v", target, err)
}

// Proxy — локальный HTTP-прокси для окна капчи (только CONNECT) и окна
// входа в диагностическом режиме (CONNECT и обычный HTTP).
type Proxy struct {
	ln     net.Listener
	dial   DialFunc
	policy proxyPolicy

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// StartProxy поднимает прокси на 127.0.0.1 со случайным портом. logf
// получает строку на каждый отказ — по ним видно, чего странице не хватило;
// nil — молчать.
func StartProxy(dial DialFunc, logf func(string, ...any)) (*Proxy, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	p, err := startProxy(dial, captchaPolicy{logf: logf})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProxy, err)
	}
	return p, nil
}

// startProxy поднимает прокси с правилами policy.
func startProxy(dial DialFunc, policy proxyPolicy) (*Proxy, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Proxy{ln: ln, dial: dial, policy: policy, ctx: ctx, cancel: cancel, conns: map[net.Conn]struct{}{}}
	p.wg.Add(1)
	go p.serve()
	return p, nil
}

// Addr — адрес прокси, «127.0.0.1:порт».
func (p *Proxy) Addr() string { return p.ln.Addr().String() }

// Close закрывает прокси и все соединения через него и ждёт их горутин.
func (p *Proxy) Close() error {
	p.cancel()
	err := p.ln.Close()
	p.mu.Lock()
	for c := range p.conns {
		c.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
	return err
}

// track запоминает соединение, чтобы Close его оборвал. false — прокси уже
// закрыт, соединение закрыто здесь же.
func (p *Proxy) track(c net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx.Err() != nil {
		c.Close()
		return false
	}
	p.conns[c] = struct{}{}
	return true
}

func (p *Proxy) untrack(c net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.conns, c)
	c.Close()
}

func (p *Proxy) serve() {
	defer p.wg.Done()
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		if !p.track(c) {
			continue
		}
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer p.untrack(c)
			p.handle(c)
		}()
	}
}

// handle обслуживает одно соединение клиента: запрос, проверка,
// соединение с сайтом, дальше — байты в обе стороны (CONNECT) или один
// запрос и ответ (обычный HTTP).
func (p *Proxy) handle(client net.Conn) {
	_ = client.SetReadDeadline(time.Now().Add(proxyHeaderTimeout))
	br := bufio.NewReader(client)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	_ = client.SetReadDeadline(time.Time{})

	if req.Method != http.MethodConnect {
		p.forward(client, req)
		return
	}
	if code := p.policy.allow(req.Host, false, req.Method); code != 0 {
		reply(client, code)
		return
	}
	upstream := p.open(client, req.Host, false)
	if upstream == nil {
		return
	}
	defer p.untrack(upstream)

	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	// Что клиент успел прислать за строкой CONNECT, лежит в br, поэтому
	// к сайту копируется из него, а не из client. Кончилась одна сторона —
	// закрываются обе: TLS поверх полузакрытого соединения не живёт.
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, br); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
	client.Close()
	upstream.Close()
	<-done
}

// open соединяется с target. Не вышло — отвечает клиенту 502 и
// возвращает nil; nil и тогда, когда прокси уже закрыт.
func (p *Proxy) open(client net.Conn, target string, plain bool) net.Conn {
	ctx, cancel := context.WithTimeout(p.ctx, proxyDialTimeout)
	upstream, err := p.dial(ctx, "tcp", target)
	cancel()
	if err != nil {
		p.policy.failed(target, plain, err)
		reply(client, http.StatusBadGateway)
		return nil
	}
	if !p.track(upstream) {
		return nil
	}
	return upstream
}

// hopHeaders — заголовки одного звена, к сайту и обратно их не передать.
var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func dropHopHeaders(h http.Header) {
	for _, name := range strings.Split(h.Get("Connection"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			h.Del(name)
		}
	}
	for _, name := range hopHeaders {
		h.Del(name)
	}
}

// forward — обычный HTTP-запрос через прокси (адрес целиком в строке
// запроса): один запрос на соединение, после ответа оба закрываются.
// Окну капчи такой не нужен — его политика отказывает.
func (p *Proxy) forward(client net.Conn, req *http.Request) {
	var target string
	if req.URL.Scheme == "http" && req.URL.Hostname() != "" {
		port := req.URL.Port()
		if port == "" {
			port = "80"
		}
		target = net.JoinHostPort(req.URL.Hostname(), port)
	}
	if code := p.policy.allow(target, true, req.Method); code != 0 {
		reply(client, code)
		return
	}
	upstream := p.open(client, target, true)
	if upstream == nil {
		return
	}
	defer p.untrack(upstream)

	dropHopHeaders(req.Header)
	req.Close = true
	if err := req.Write(upstream); err != nil {
		reply(client, http.StatusBadGateway)
		return
	}
	resp, err := http.ReadResponse(bufio.NewReader(upstream), req)
	if err != nil {
		reply(client, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	dropHopHeaders(resp.Header)
	resp.Close = true
	_ = resp.Write(client)
}

// reply отвечает клиенту кодом без тела и закрывает разговор.
func reply(c net.Conn, code int) {
	fmt.Fprintf(c, "HTTP/1.1 %d %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", code, http.StatusText(code))
}
