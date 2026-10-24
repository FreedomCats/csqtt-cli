module github.com/FreedomCats/csqtt-cli

go 1.26.2

// golang.org/x/sys (BSD-3) — системные вызовы Windows и Linux, DPAPI.
// golang.org/x/term (BSD-3) — скрытый ввод секретов.
// golang.org/x/crypto (BSD-3) — cryptobyte: разбор подписи wintun.dll в
// scripts/internal/wintun.
// golang.zx2c4.com/wireguard/windows (MIT) — пакет tunnel/winipcfg: маршруты,
// адреса, DNS и выбор интерфейса. Импортируется только из *_windows.go,
// в сборки под другие системы не попадает. Сам WireGuard не используется.
// golang.zx2c4.com/wireguard (MIT) — только пакет tun: адаптер Wintun
// datapath tun на Windows; тянет golang.zx2c4.com/wintun (MIT). Импортируется
// только из *_windows.go. Версия — та, что в qwdtt-cli.
// tailscale/wf (BSD-3) — фильтры WFP защиты от утечки DNS и kill-switch;
// тянет go4.org/netipx (BSD-3). Тоже только из *_windows.go. Прочие его
// косвенные (x/tools, honnef.co/go/tools, x/exp/typeparams) нужны лишь его
// генераторам и в бинарь не попадают.
// vishvananda/netlink (Apache-2.0) — выбор интерфейса и подписка на смену
// сети на Linux; тянет vishvananda/netns (Apache-2.0). Только из *_linux.go.
// google/nftables (Apache-2.0) — защита от утечки DNS на Linux; тянет
// mdlayher/netlink и mdlayher/socket (MIT). Взят из ветки main, а не из
// релиза: флаг таблицы owner (таблица уходит вместе с процессом) есть только
// там. Только из *_linux.go. go-cmp (BSD-3) нужен лишь тестам mdlayher/netlink
// и в бинарь не попадает.
// godbus/dbus (BSD-2) — DNS туннеля через systemd-resolved на Linux и
// секреты в связке ключей (Secret Service). Только из *_linux.go.
// gvisor.dev/gvisor (Apache-2.0) — netstack datapath socks; тянет google/btree
// (Apache-2.0) и x/time (BSD-3). Версия — та, на которой стоял netstack
// wireguard-go в qwdtt-cli: проверена с этим Go. Модуль огромный, но в бинарь
// попадают только пакеты pkg/tcpip и их опоры.
// golang.org/x/net (BSD-3) — dnsmessage: резолвер имён SOCKS5 через туннель;
// proxy — только тестам, сторонний клиент SOCKS5.
// BurntSushi/toml (MIT) — разбор profiles.toml.
// jchv/go-webview2 (MIT) — окно капчи на Windows. Без cgo, WebView2Loader.dll
// встроен; тянет jchv/go-winloader (MIT). Только из *_windows.go.
require (
	github.com/BurntSushi/toml v1.6.0
	github.com/godbus/dbus/v5 v5.1.0
	github.com/google/nftables v0.3.1-0.20260430172505-f9b52ed2ba65
	github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808
	github.com/tailscale/wf v0.0.0-20240214030419-6fbb0a674ee6
	github.com/vishvananda/netlink v1.3.1
	golang.org/x/crypto v0.54.0
	golang.org/x/net v0.57.0
	golang.org/x/sys v0.47.0
	golang.org/x/term v0.45.0
	golang.zx2c4.com/wireguard v0.0.0-20260522210424-ecfc5a8d5446
	golang.zx2c4.com/wireguard/windows v1.1.1
	gvisor.dev/gvisor v0.0.0-20250503011706-39ed1f5ac29c
)

require (
	github.com/google/btree v1.1.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	github.com/mdlayher/netlink v1.8.1-0.20251028132421-dcc6cab9a6eb // indirect
	github.com/mdlayher/socket v0.5.1 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	go4.org/netipx v0.0.0-20220725152314-7e7bdc8411bf // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/time v0.7.0 // indirect
	golang.org/x/tools v0.47.0 // indirect
	golang.org/x/tools/go/expect v0.1.1-deprecated // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	honnef.co/go/tools v0.5.1 // indirect
)

// tailscale/wf — из своего форка: арена библиотеки отдавала невыровненную
// память, и запись правила WFP во время разметки GC роняла процесс
// («bulkBarrierPreWrite: unaligned arguments»). В форке одна правка,
// malloc.go, с тестом; апстрим с 2024 года не развивается. Регрессия у нас —
// TestDNSGuardUnderGC в internal/winsys.
replace github.com/tailscale/wf => github.com/FreedomCats/tailscale-wf v0.0.0-20261016000000-b03557658b85

// jchv/go-webview2 — из своего форка: библиотека не передаёт опций окружения
// WebView2, а переменную WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS рантайм с её
// встроенным загрузчиком не применяет — окно капчи шло бы мимо прокси
// csqtt-cli. В форке одна правка: опция AdditionalBrowserArguments через
// ICoreWebView2EnvironmentOptions, с тестом. Регрессия у нас — TestWebView2Proxy
// в internal/captcha (CSQTT_WEBVIEW2=1).
replace github.com/jchv/go-webview2 => github.com/FreedomCats/go-webview2 v0.0.0-20261014000000-08b745a2490c
