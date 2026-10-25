package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withSockets подменяет таблицу сторожа на время теста.
func withSockets(t *testing.T, ss ...socketSite) {
	t.Helper()
	saved := socketSites
	t.Cleanup(func() { socketSites = saved })
	socketSites = ss
}

// corePlan — core/ репозитория как план переноса: путь → содержимое.
func corePlan(t *testing.T) map[string][]byte {
	t.Helper()
	core := filepath.Join("..", "..", "core")
	files, err := listFiles(core, ".")
	if err != nil {
		t.Fatal(err)
	}
	plan := map[string][]byte{}
	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(core, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		plan[rel] = normalize(raw)
	}
	return plan
}

// Таблица сторожа сходится с core/ репозитория — тот же вызов, что в
// переносе и -check.
func TestSocketGuardOnCore(t *testing.T) {
	if err := checkSockets(corePlan(t)); err != nil {
		t.Error(err)
	}
}

// Новый сокет, снятая привязка, пропавшее покрытое место — ошибка с файлом и
// строкой.
func TestSocketGuardCatches(t *testing.T) {
	cases := map[string]struct {
		spoil func(plan map[string][]byte)
		want  []string
	}{
		"новый сокет в файле апстрима": {
			spoil: func(plan map[string][]byte) {
				plan["rust-client/worker.rs"] = append(plan["rust-client/worker.rs"],
					"\nfn probe() {\n    let _ = std::net::UdpSocket::bind(\"0.0.0.0:0\");\n}\n"...)
			},
			want: []string{"rust-client/worker.rs:", "UdpSocket::bind — новое место"},
		},
		"новый файл апстрима": {
			spoil: func(plan map[string][]byte) {
				plan["shared/leak.rs"] = []byte("async fn f() {\n    tokio::net::TcpStream::connect(addr).await;\n}\n")
			},
			want: []string{"shared/leak.rs:2: TcpStream::connect — новое место"},
		},
		"новый клиент primp": {
			spoil: func(plan map[string][]byte) {
				plan["rust-client/captcha.rs"] = append(plan["rust-client/captcha.rs"],
					"\nfn client() { primp::Client::builder().build(); }\n"...)
			},
			want: []string{"rust-client/captcha.rs:", "Client::builder — новое место"},
		},
		// Клиенты без построителя: имя — через getaddrinfo, прокси — из
		// окружения, мимо привязки.
		"primp::get": {
			spoil: func(plan map[string][]byte) {
				plan["shared/leak.rs"] = []byte("async fn f(url: &str) {\n    let _ = primp::get(url).await;\n}\n")
			},
			want: []string{"shared/leak.rs:2: primp::get — новое место"},
		},
		"reqwest::blocking::get": {
			spoil: func(plan map[string][]byte) {
				plan["shared/leak.rs"] = []byte("fn f(url: &str) {\n    let _ = reqwest::blocking::get(url);\n}\n")
			},
			want: []string{"shared/leak.rs:2: reqwest::blocking::get — новое место"},
		},
		"Client::new, Client::default, ClientBuilder::default": {
			spoil: func(plan map[string][]byte) {
				plan["rust-client/worker.rs"] = append(plan["rust-client/worker.rs"],
					"\nfn clients() {\n    let _ = primp::Client::new();\n    let _ = reqwest::Client::default();\n    let _ = ClientBuilder::default().build();\n}\n"...)
			},
			want: []string{
				"Client::new — новое место",
				"Client::default — новое место",
				"ClientBuilder::default — новое место",
			},
		},
		"тестовый модуль без конца": {
			spoil: func(plan map[string][]byte) {
				plan["rust-client/worker.rs"] = append(plan["rust-client/worker.rs"],
					"\n#[cfg(test)]\nmod more_tests {\n    fn t() {}\n  }\nfn after() { UdpSocket::bind(a); }\n"...)
			},
			want: []string{"rust-client/worker.rs:", `у тестового модуля "mod more_tests {" нет закрывающей строки "}"`, "файл не проверен"},
		},
		"второй сокет в коннекторе": {
			spoil: func(plan map[string][]byte) {
				const f = "rust-client/vendor/primp-hyper-util/src/client/legacy/connect/http.rs"
				plan[f] = append(plan[f], "\nfn more() { Socket::new(domain, Type::STREAM, None); }\n"...)
			},
			want: []string{"connect/http.rs: Socket::new — найдено 2, в socketSites 1"},
		},
		"привязку сняли": {
			spoil: func(plan map[string][]byte) {
				const f = "rust-client/vendor/primp-hyper-util/src/client/legacy/connect/http.rs"
				plan[f] = []byte(strings.Replace(string(plan[f]), "csqtt_bind::bind_socket(", "drop(", 1))
			},
			want: []string{"connect/http.rs:", "нет csqtt_bind::bind_socket( — сокет без привязки"},
		},
		"клиент без резолвера ядра": {
			spoil: func(plan map[string][]byte) {
				const f = "rust-client/auth.rs"
				plan[f] = []byte(strings.Replace(string(plan[f]), ".dns_resolver(dns::global())", "", 1))
			},
			want: []string{"rust-client/auth.rs:", "нет .dns_resolver(dns::global())"},
		},
		"точку переписали": {
			spoil: func(plan map[string][]byte) {
				const f = "rust-client/dns.rs"
				plan[f] = []byte(strings.Replace(string(plan[f]), "crate::bind_if::udp_socket(", "udp(", 1))
			},
			want: []string{"rust-client/dns.rs: bind_if::udp_socket — найдено 0, в socketSites 1"},
		},
		// Маршрутизация мимо туннеля: вернулся голый вызов апстрима.
		"прямое TCP без привязки": {
			spoil: func(plan map[string][]byte) {
				const f = "rust-client/direct_relay.rs"
				plan[f] = []byte(strings.Replace(string(plan[f]), "crate::bind_if::connect_tcp(remote_addr)", "tokio::net::TcpStream::connect(remote_addr)", 1))
			},
			want: []string{
				"rust-client/direct_relay.rs:", "TcpStream::connect — новое место",
				"rust-client/direct_relay.rs: bind_if::connect_tcp — найдено 0, в socketSites 1",
			},
		},
		"прямой UDP без привязки": {
			spoil: func(plan map[string][]byte) {
				const f = "rust-client/direct_relay.rs"
				plan[f] = []byte(strings.Replace(string(plan[f]), "crate::bind_if::udp_socket(bind_addr)", "tokio::net::UdpSocket::bind(bind_addr)", 1))
			},
			want: []string{
				"rust-client/direct_relay.rs:", "UdpSocket::bind — новое место",
				"rust-client/direct_relay.rs: bind_if::udp_socket — найдено 1, в socketSites 2",
			},
		},
		"предразрешение без резолвера ядра": {
			spoil: func(plan map[string][]byte) {
				const f = "rust-client/direct_relay.rs"
				plan[f] = []byte(strings.Replace(string(plan[f]), "crate::dns::global()", "resolver()", 1))
			},
			want: []string{"rust-client/direct_relay.rs:", "после lookup_host в той же функции нет crate::dns::global()"},
		},
		"файл пропал": {
			spoil: func(plan map[string][]byte) { delete(plan, "rust-client/tun.rs") },
			want:  []string{"rust-client/tun.rs: файла нет"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			plan := corePlan(t)
			c.spoil(plan)
			err := checkSockets(plan)
			if err == nil {
				t.Fatal("сторож промолчал")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("в ошибке нет %q:\n%v", w, err)
				}
			}
		})
	}
}

// Тесты в сборку не входят и сторожа не касаются: модули testCfg, файлы
// *_tests.rs, тесты и примеры вендоренного крейта. Комментарии — тоже. А
// #[cfg(not(test))] — код сборки.
func TestSocketGuardSkipsTests(t *testing.T) {
	withSockets(t)
	withCrates(t, fakeCrate)
	quiet := map[string][]byte{
		"rust-client/a.rs":                             []byte("fn f() {}\n\n#[cfg(test)]\nmod tests {\n    fn t() { UdpSocket::bind(a); }\n}\n"),
		"rust-client/b.rs":                             []byte("#[cfg(all(test, unix))]\nmod tests {\n    use nix::sys::socket::socket;\n    fn t() { socket(a); }\n\n    mod inner {\n        fn u() {}\n    }\n}\n"),
		"rust-client/c.rs":                             []byte("    // TcpStream::connect(a)\n    /// let c = Client::builder();\nfn socket(x: u8) {}\n"),
		"rust-client/d.rs":                             []byte("mod outer {\n    #[cfg(test)]\n    mod tests {\n        fn t() { TcpListener::bind(a); }\n    }\n}\n"),
		"rust-client/turn_integration_tests.rs":        []byte("fn t() { UdpSocket::bind(a); }\n"),
		"rust-client/vendor/fake-util/tests/x.rs":      []byte("fn t() { TcpStream::connect(a); }\n"),
		"rust-client/vendor/fake-util/examples/e.rs":   []byte("fn t() { TcpStream::connect(a); }\n"),
		"rust-client/Cargo.toml":                       []byte("UdpSocket::bind(\n"),
		"rust-client/self_socket.rs":                   []byte("fn f() { self.socket(); TcpSocket::new_v4(); }\n"),
		"rust-client/vendor/fake-util/src/not_rust.md": []byte("TcpStream::connect(a)\n"),
	}
	// self.socket() — не socket() из nix, а TcpSocket::new_v4 — не Socket::new.
	if err := checkSockets(quiet); err == nil || strings.Count(err.Error(), "новое место") != 1 || !strings.Contains(err.Error(), "self_socket.rs:1: TcpSocket::new_v4") {
		t.Errorf("ожидалось одно место — TcpSocket::new_v4 в self_socket.rs: %v", err)
	}

	loud := map[string][]byte{
		"rust-client/a.rs": []byte("#[cfg(not(test))]\nmod real {\n    fn f() { UdpSocket::bind(a); }\n}\n"),
	}
	if err := checkSockets(loud); err == nil || !strings.Contains(err.Error(), "rust-client/a.rs:3: UdpSocket::bind") {
		t.Errorf("#[cfg(not(test))] пропущен: %v", err)
	}
}
