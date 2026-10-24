package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Сторож правки П3 (doc/architecture.md §06): каждый сокет ядра наружу
// привязан к физическому интерфейсу. checkSockets ищет в коде, который
// попадёт в core/, все места, где создаётся сокет или HTTP-клиент, и сверяет
// их с socketSites. Новое место — ошибка переноса и -check: апстрим завёл
// сокет, а привязки у него нет, и при tun он ушёл бы в туннель.

// socketSite — место, где ядро создаёт сокет, или вызов, которым это делают.
type socketSite struct {
	// File — путь в core/; Call — вызов, как его находит socketCall.
	File, Call string
	// Count — сколько таких вызовов в файле.
	Count int
	// Then — что обязано стоять в той же функции после каждого вызова: так
	// сокет привязывается. Пусто — привязка не нужна или делается иначе, см.
	// Reason.
	Then []string
	// Reason — что это за сокет и почему он покрыт.
	Reason string
}

// socketSites — все места создания сокетов в core/ после правил.
var socketSites = []socketSite{
	// Пять точек П3 (§06). Сокеты точек 1–3 создаёт bind_if.rs, соединения
	// точек 4–5 — коннектор primp-hyper-util.
	{File: "rust-client/dns.rs", Call: "bind_if::udp_socket", Count: 1,
		Reason: "точка 1: DNS для имён VK, UDP к DNS Яндекса"},
	{File: "rust-client/turn.rs", Call: "bind_if::udp_socket", Count: 1,
		Reason: "точка 2: TURN по UDP"},
	{File: "rust-client/turn_stream.rs", Call: "bind_if::connect_tcp", Count: 1,
		Reason: "точка 3: TURN по TCP и TLS"},
	{File: "rust-client/auth.rs", Call: "bind_if::client_builder", Count: 1, Then: []string{".dns_resolver(dns::global())"},
		Reason: "точка 4: API VK; имена разрешает точка 1, а не getaddrinfo"},
	{File: "rust-client/vk_js_calls.rs", Call: "bind_if::client_builder", Count: 1, Then: []string{".dns_resolver(crate::dns::global())"},
		Reason: "точка 5: API звонков VK (auto_js); имена разрешает точка 1"},
	{File: "rust-client/vendor/primp-hyper-util/src/client/legacy/connect/http.rs", Call: "Socket::new", Count: 1,
		Then:   []string{"csqtt_bind::bind_socket("},
		Reason: "точки 4–5: сокет каждого соединения primp"},

	// Сами вызовы bind_if.rs.
	{File: "rust-client/bind_if.rs", Call: "UdpSocket::bind", Count: 1, Then: []string{"bind(&socket"},
		Reason: "udp_socket — точки 1 и 2"},
	{File: "rust-client/bind_if.rs", Call: "TcpSocket::new_v4", Count: 1, Then: []string{"bind(&socket"},
		Reason: "connect_tcp — точка 3"},
	{File: "rust-client/bind_if.rs", Call: "TcpSocket::new_v6", Count: 1, Then: []string{"bind(&socket"},
		Reason: "connect_tcp — точка 3"},
	{File: "rust-client/bind_if.rs", Call: "Client::builder", Count: 1, Then: []string{"no_proxy()"},
		Reason: "client_builder — точки 4 и 5"},
	{File: "rust-client/bind_if.rs", Call: "Socket::new", Count: 1, Then: []string{"bind_socket_to("},
		Reason: "пробный сокет проверки -bind-if и BIND_IF: никуда не шлёт"},

	// Привязка не нужна.
	{File: "rust-client/dispatcher.rs", Call: "UdpSocket::bind", Count: 2,
		Reason: "сокет -listen на loopback: канал пакетов оболочки, наружу не ходит"},
	{File: "rust-client/tun.rs", Call: "socket", Count: 1,
		Reason: "абстрактный unix-сокет -tun-uds для дескриптора TUN"},
	{File: "rust-client/vendor/primp-hyper-util/src/client/legacy/connect/http.rs", Call: "TcpSocket::from_std_stream", Count: 1,
		Reason: "тот же сокет из Socket::new, уже привязанный"},
	{File: "rust-client/vendor/primp/src/lib.rs", Call: "ClientBuilder::new", Count: 3,
		Reason: "обёртка primp: построитель; соединения клиента привязывает коннектор"},
	{File: "rust-client/vendor/primp/src/lib.rs", Call: "Client::new", Count: 1,
		Reason: "обёртка primp: текст ошибки в Client::new; ядро клиентов так не строит"},
	{File: "rust-client/vendor/primp/src/lib.rs", Call: "Client::builder", Count: 1,
		Reason: "обёртка primp: primp::get; ядро его не вызывает"},
	{File: "rust-client/vendor/netstack-smoltcp/src/tcp.rs", Call: "TcpSocket::new", Count: 1,
		Reason: "smoltcp::socket::tcp::Socket — виртуальный сокет стека в памяти (маршрутизация мимо туннеля), не сокет ОС"},

	// Мимо привязки — по решению (§06).
	{File: "rust-client/dns.rs", Call: "lookup_host", Count: 1,
		Reason: "запасной системный DNS: только если DNS Яндекса молчит, а TUNCONF ещё не было; запрос делает система (W5, Э3)"},

	// Маршрутизация мимо туннеля (ядро 2.5.0: --domain-routing, --ip-routing,
	// команда UPDATE_DOMAINS|): оболочка её не включает, и без неё ни один из
	// этих сокетов не создаётся — предразрешение доменов идёт только при
	// enabled, остальное — только из try_handle, который без маршрутизации
	// сразу возвращает false. Включать её — отдельная задача вместе с
	// привязкой (решение пользователя при обновлении на 2.5.0).
	{File: "rust-client/direct_relay.rs", Call: "lookup_host", Count: 1,
		Reason: "предразрешение прямых доменов: только при маршрутизации мимо туннеля, которую оболочка не включает"},
	{File: "rust-client/direct_relay.rs", Call: "TcpStream::connect", Count: 1,
		Reason: "прямое TCP-соединение из стека smoltcp: только при маршрутизации мимо туннеля, которую оболочка не включает"},
	{File: "rust-client/direct_relay.rs", Call: "UdpSocket::bind", Count: 2,
		Reason: "прямые UDP-сессии и DNS-запросы для прямых доменов: только при маршрутизации мимо туннеля, которую оболочка не включает"},
	{File: "rust-client/vendor/primp-hyper-util/src/client/legacy/connect/dns.rs", Call: "to_socket_addrs", Count: 1,
		Reason: "GaiResolver: в ядре заменён резолвером dns_resolver(dns::global())"},
	{File: "rust-client/vendor/primp-hyper-util/src/client/legacy/connect/proxy/socks/v4/mod.rs", Call: "to_socket_addrs", Count: 1,
		Reason: "SOCKS-прокси: прокси берутся только из окружения, а с -bind-if выключены"},
	{File: "rust-client/vendor/primp-hyper-util/src/client/legacy/connect/proxy/socks/v5/mod.rs", Call: "to_socket_addrs", Count: 1,
		Reason: "SOCKS-прокси: прокси берутся только из окружения, а с -bind-if выключены"},
}

// socketCall — вызов, создающий сокет, разрешающий имя или HTTP-клиент.
// Клиент — и без построителя: primp::get и reqwest::get (в том числе
// blocking) строят клиента сами, Client::new и Client::default — тоже; такой
// клиент разрешил бы имя через getaddrinfo и взял бы прокси из окружения.
// Группа 1 — сам вызов: перед ним не буква, не цифра и не точка, иначе
// TcpSocket::new_v4 нашёлся бы ещё и как Socket::new, а self.socket() — как
// socket() из nix. to_socket_addrs — метод, он ищется с точкой.
var socketCall = regexp.MustCompile(`(?:^|[^\w.])((?:UdpSocket|TcpListener|UnixListener|UnixDatagram)::bind|(?:UdpSocket|TcpStream|TcpListener)::from_std|TcpStream::connect(?:_timeout)?|UnixStream::connect|TcpSocket::(?:new_v4|new_v6|new|from_std_stream)|Socket::(?:new|new_raw|pair)|UnixDatagram::(?:unbound|pair)|socket|lookup_host|Client::(?:builder|new|default)|ClientBuilder::(?:new|default)|(?:primp|reqwest)(?:::blocking)?::get|bind_if::(?:udp_socket|connect_tcp|client_builder))\(|\.(to_socket_addrs)\(`)

// testCfg — атрибут модуля, который собирается только в тестах:
// #[cfg(test)] или #[cfg(all(test, …))], но не #[cfg(all(not(test), …))].
var testCfg = regexp.MustCompile(`^#\[cfg\((?:test|all\((?:[^()]|\([^()]*\))*\btest\b(?:[^()]|\([^()]*\))*\))\)\]$`)

// socketFn — объявление функции socket, а не вызов socket() из nix.
var socketFn = regexp.MustCompile(`\bfn\s+socket\(`)

// fnStart — начало функции: до него ищутся Then.
var fnStart = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:const\s+)?(?:async\s+)?(?:unsafe\s+)?(?:extern\s+"[^"]*"\s+)?fn\s`)

// scannedForSockets — попадает ли файл core/ под сторожа. Не попадают
// тестовые модули ядра (*_tests.rs, их подключают #[cfg(test)]) и тесты и
// примеры вендоренных крейтов: в сборку ядра они не входят.
func scannedForSockets(rel string) bool {
	if !strings.HasSuffix(rel, ".rs") || strings.HasSuffix(rel, "_tests.rs") {
		return false
	}
	for _, c := range vendoredCrates {
		if strings.HasPrefix(rel, c.Dir+"/tests/") || strings.HasPrefix(rel, c.Dir+"/examples/") {
			return false
		}
	}
	return slices.ContainsFunc(vendoredDirs, func(dir string) bool { return strings.HasPrefix(rel, dir+"/") })
}

// codeLines — строки файла без комментариев-строк и без тестовых модулей
// (testCfg): тесты в сборку не входят. Вырезанные строки становятся
// пустыми, чтобы номера строк сохранились. Конец модуля — строка "}" с тем
// же отступом, что у "mod …{": так его пишет rustfmt. Нет такой строки —
// ошибка: иначе остаток файла молча выпал бы из проверки.
func codeLines(text string) ([]string, error) {
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "//") {
			lines[i] = ""
			continue
		}
		if !testCfg.MatchString(trimmed) || i+1 >= len(lines) {
			continue
		}
		indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
		next := strings.TrimSpace(lines[i+1])
		if !strings.HasPrefix(next, "mod ") || !strings.HasSuffix(next, "{") {
			continue
		}
		end := i + 1
		for end < len(lines) && lines[end] != indent+"}" {
			end++
		}
		if end == len(lines) {
			return nil, fmt.Errorf("%d: у тестового модуля %q нет закрывающей строки %q — сторож не отличит тесты от кода сборки; файл не проверен (sockets.go, codeLines)", i+2, next, indent+"}")
		}
		for j := i; j <= end; j++ {
			lines[j] = ""
		}
		i = end
	}
	return lines, nil
}

// socketCalls — вызовы socketCall в файле: вызов → номера строк (с нуля).
func socketCalls(lines []string) map[string][]int {
	calls := map[string][]int{}
	for i, line := range lines {
		for _, m := range socketCall.FindAllStringSubmatch(line, -1) {
			call := m[1] + m[2]
			if call == "socket" && socketFn.MatchString(line) {
				continue
			}
			calls[call] = append(calls[call], i)
		}
	}
	return calls
}

// checkSockets сверяет места создания сокетов в plan с socketSites.
func checkSockets(plan map[string][]byte) error {
	var problems []string
	known := map[[2]string]bool{}
	for _, s := range socketSites {
		known[[2]string{s.File, s.Call}] = true
		if _, ok := plan[s.File]; !ok {
			problems = append(problems, fmt.Sprintf("%s: файла нет, а в socketSites он есть (%s)", s.File, s.Call))
		}
	}

	var names []string
	for name := range plan {
		if scannedForSockets(name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		lines, err := codeLines(string(plan[name]))
		if err != nil {
			problems = append(problems, name+":"+err.Error())
			continue
		}
		calls := socketCalls(lines)
		for call, at := range calls {
			if !known[[2]string{name, call}] {
				for _, i := range at {
					problems = append(problems, fmt.Sprintf("%s:%d: %s — новое место, где ядро создаёт сокет; привяжите его (П3) и внесите в socketSites", name, i+1, call))
				}
			}
		}
		for _, s := range socketSites {
			if s.File != name {
				continue
			}
			at := calls[s.Call]
			if len(at) != s.Count {
				problems = append(problems, fmt.Sprintf("%s: %s — найдено %d, в socketSites %d; строки %s", name, s.Call, len(at), s.Count, lineNumbers(at)))
				continue
			}
			for _, i := range at {
				body := functionTail(lines, i)
				for _, then := range s.Then {
					if !strings.Contains(body, then) {
						problems = append(problems, fmt.Sprintf("%s:%d: после %s в той же функции нет %s — сокет без привязки", name, i+1, s.Call, then))
					}
				}
			}
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return fmt.Errorf("сторож П3 — сокеты ядра без привязки к интерфейсу (scripts/vendorcore/sockets.go):\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

// functionTail — текст от строки from до начала следующей функции.
func functionTail(lines []string, from int) string {
	end := from + 1
	for end < len(lines) && !fnStart.MatchString(lines[end]) {
		end++
	}
	return strings.Join(lines[from:end], "\n")
}

func lineNumbers(at []int) string {
	if len(at) == 0 {
		return "—"
	}
	s := make([]string, len(at))
	for i, n := range at {
		s[i] = fmt.Sprint(n + 1)
	}
	return strings.Join(s, ", ")
}
