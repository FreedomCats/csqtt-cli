package coreproc

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestArgs(t *testing.T) {
	cfg := Config{
		Peer:          "203.0.113.10:46000",
		Password:      "секрет",
		Hashes:        []string{"-Wabc", "def"},
		Workers:       50,
		Obfs:          "video",
		TurnTransport: "tcp_tls",
		ClientIDs:     []string{"1", "2"},
		Listen:        "127.0.0.1:0",
		ListenPeer:    "127.0.0.1:40000",
		BindIf:        7,
		Mark:          0x5153,
	}
	want := []string{
		"--secrets-stdin",
		"--peer=203.0.113.10:46000",
		"--workers=50",
		"--obfs=video",
		"--turn-transport=tcp_tls",
		"--client-ids=1,2",
		"--listen=127.0.0.1:0",
		"--listen-peer=127.0.0.1:40000",
		"--bind-if=7",
		"--mark=20819",
	}
	if got := cfg.Args(); !slices.Equal(got, want) {
		t.Errorf("Args() = %q, want %q", got, want)
	}
}

// Встроенный список ядра мимо туннеля — два голых флага (clap ядра значения
// у булевых не принимает); без опции флагов маршрутизации нет вовсе.
func TestArgsTemplateDomains(t *testing.T) {
	routing := func(args []string) []string {
		var got []string
		for _, a := range args {
			if strings.HasPrefix(a, "--domain") || strings.HasPrefix(a, "--template") ||
				strings.HasPrefix(a, "--ip-") || strings.HasPrefix(a, "--direct") || strings.HasPrefix(a, "--exclude") {
				got = append(got, a)
			}
		}
		return got
	}
	cfg := Config{Peer: "203.0.113.10:46000"}
	if got := routing(cfg.Args()); got != nil || cfg.DirectRouting() {
		t.Errorf("без опции: флаги %q, DirectRouting %v", got, cfg.DirectRouting())
	}
	cfg.TemplateDomains = true
	if got, want := routing(cfg.Args()), []string{"--domain-routing", "--template-domains"}; !slices.Equal(got, want) {
		t.Errorf("с опцией: флаги %q, want %q", got, want)
	}
	if !cfg.DirectRouting() {
		t.Error("с опцией DirectRouting = false")
	}
}

// Формат — тот, что разбирает secrets_stdin.rs ядра: ровно два поля строками.
func TestSecretsLine(t *testing.T) {
	cfg := Config{Password: `се"крет<`, Hashes: []string{"-Wabc", "def"}}
	line := cfg.SecretsLine()
	encoded, ok := strings.CutPrefix(line, "SECRETS:")
	if !ok || strings.ContainsAny(line, "\r\n") {
		t.Fatalf("SecretsLine() = %q", line)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"password": `се"крет<`, "vk": "-Wabc,def"}; !reflect.DeepEqual(got, want) {
		t.Errorf("json = %v, want %v", got, want)
	}
}

// Те же значения, что в worker_count_tests ядра (main.rs).
func TestNormalizeWorkers(t *testing.T) {
	for groups := 1; groups <= maxWorkers/workersPerGroup; groups++ {
		if n := groups * workersPerGroup; NormalizeWorkers(n) != n {
			t.Errorf("NormalizeWorkers(%d) = %d", n, NormalizeWorkers(n))
		}
	}
	for requested := -1; requested <= 1000; requested++ {
		n := NormalizeWorkers(requested)
		if requested <= minWorkers {
			if n != minWorkers {
				t.Errorf("NormalizeWorkers(%d) = %d, want %d", requested, n, minWorkers)
			}
			continue
		}
		if n < workersPerGroup || n > maxWorkers || n%workersPerGroup != 0 {
			t.Errorf("NormalizeWorkers(%d) = %d", requested, n)
		}
	}
	for _, c := range []struct{ requested, want int }{
		{math.MaxInt, 100}, {math.MaxInt - 1, 100}, {126, 100}, {48, 40}, {18, 10}, {9, 10}, {5, 5}, {4, 5},
	} {
		if got := NormalizeWorkers(c.requested); got != c.want {
			t.Errorf("NormalizeWorkers(%d) = %d, want %d", c.requested, got, c.want)
		}
	}
	// Предела на хеш у ядра 2.5.0 нет: хеши на число воркеров не влияют.
	if got := (Config{Workers: 50, Hashes: []string{"h"}}).NormalizedWorkers(); got != 50 {
		t.Errorf("NormalizedWorkers(50, 1 хеш) = %d, want 50", got)
	}
}
