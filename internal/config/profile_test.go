package config

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
)

func TestCoreMapping(t *testing.T) {
	got := parse(t, fullFile).Profiles["home"].Core()
	want := coreproc.Config{
		Peer:          "203.0.113.10:46000",
		Workers:       27,
		Obfs:          "video",
		TurnTransport: "tcp_tls",
		DeviceID:      "3f9c2a71d04be658",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Core = %+v,\nожидалось %+v", got, want)
	}
}

// Пропущенное в профиле ядро возьмёт своё; оболочка подставляет только
// воркеров (coreproc.NormalizeWorkers из нуля сделал бы девять) и порт
// сервера.
func TestCoreDefaults(t *testing.T) {
	got := parse(t, minimalFile).Profiles["home"].Core()
	want := coreproc.Config{Peer: "203.0.113.10:" + DefaultPeerPort, Workers: DefaultWorkers}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Core = %+v,\nожидалось %+v", got, want)
	}
	if args := got.Args(); !slices.Contains(args, "--workers=18") {
		t.Errorf("аргументы ядра без --workers=18: %q", args)
	}
}

// Секретов и адресов канала пакетов в Core нет, и выбор datapath на
// параметры ядра не влияет: их задаёт сам datapath.
func TestCoreIgnoresDatapath(t *testing.T) {
	p := parse(t, fullFile).Profiles["home"]
	socks := p.Core()
	p.Datapath, p.Socks.Listen = DatapathTun, ""
	if tun := p.Core(); !reflect.DeepEqual(socks, tun) {
		t.Errorf("datapath просочился в параметры ядра: %+v против %+v", socks, tun)
	}
	if socks.Password != "" || socks.Hashes != nil || socks.Listen != "" || socks.ListenPeer != "" {
		t.Errorf("Core заполнил то, что не его: %+v", socks)
	}
}

func TestPeerWithPort(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"203.0.113.10":        "203.0.113.10:46000",
		" vps.example ":       "vps.example:46000",
		"vps.example:1":       "vps.example:1",
		"[2001:db8::1]":       "[2001:db8::1]:46000",
		"[2001:db8::1]:46001": "[2001:db8::1]:46001",
		// Голый IPv6 не трогается: проверка попросит скобки.
		"2001:db8::1": "2001:db8::1",
	}
	for in, want := range cases {
		if got := PeerWithPort(in); got != want {
			t.Errorf("PeerWithPort(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}

func TestProfileByName(t *testing.T) {
	profile, err := parse(t, fullFile).Profile("work")
	if err != nil {
		t.Fatalf("Profile(work): %v", err)
	}
	if profile.Name != "work" {
		t.Errorf("выбран профиль %q", profile.Name)
	}
}

func TestProfileDefault(t *testing.T) {
	profile, err := parse(t, fullFile).Profile("")
	if err != nil {
		t.Fatalf("Profile(\"\"): %v", err)
	}
	if profile.Name != "home" {
		t.Errorf("по умолчанию выбран %q, ожидался home", profile.Name)
	}
}

// Единственный профиль и есть профиль по умолчанию: заставлять писать
// default_profile, когда выбирать не из чего, незачем.
func TestProfileSingleIsDefault(t *testing.T) {
	profile, err := parse(t, minimalFile).Profile("")
	if err != nil {
		t.Fatalf("Profile(\"\"): %v", err)
	}
	if profile.Name != "home" {
		t.Errorf("выбран %q", profile.Name)
	}
}

func TestProfileAmbiguousDefault(t *testing.T) {
	file := parse(t, strings.Replace(fullFile, `default_profile = "home"`, "", 1))

	_, err := file.Profile("")
	if err == nil {
		t.Fatal("профиль выбран, хотя их два и default_profile не задан")
	}
	for _, want := range []string{"default_profile", "home", "work"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в сообщении нет %q: %v", want, err)
		}
	}
}

// Промах с именем — почти всегда опечатка, поэтому в сообщении список имён:
// иначе за ним придётся идти в файл.
func TestProfileUnknownName(t *testing.T) {
	_, err := parse(t, fullFile).Profile("hom")
	if err == nil {
		t.Fatal("несуществующий профиль найден")
	}
	for _, want := range []string{"hom", "home", "work"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в сообщении нет %q: %v", want, err)
		}
	}
}

func TestProfileEmptyFile(t *testing.T) {
	file := parse(t, "\n")

	if _, err := file.Profile(""); err == nil {
		t.Error("пустой файл отдал профиль по умолчанию")
	}
	if _, err := file.Profile("home"); err == nil {
		t.Error("пустой файл отдал профиль по имени")
	}
}

func TestEffectiveDatapath(t *testing.T) {
	cases := map[string]string{
		"":        DefaultDatapath,
		"  ":      DefaultDatapath,
		"socks":   DatapathSocks,
		" tun ":   DatapathTun,
		"unknown": "unknown", // отвергает Validate
	}
	for in, want := range cases {
		if got := EffectiveDatapath(in); got != want {
			t.Errorf("EffectiveDatapath(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}
