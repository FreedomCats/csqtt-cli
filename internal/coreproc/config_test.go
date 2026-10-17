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
		DeviceID:      "dev",
		Listen:        "127.0.0.1:0",
	}
	want := []string{
		"--secrets-stdin",
		"--peer=203.0.113.10:46000",
		"--workers=45",
		"--obfs=video",
		"--turn-transport=tcp_tls",
		"--client-ids=1,2",
		"--device-id=dev",
		"--listen=127.0.0.1:0",
	}
	if got := cfg.Args(); !slices.Equal(got, want) {
		t.Errorf("Args() = %q, want %q", got, want)
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
		if n := groups * workersPerGroup; NormalizeWorkers(n, maxHashes) != n {
			t.Errorf("NormalizeWorkers(%d) = %d", n, NormalizeWorkers(n, maxHashes))
		}
	}
	for requested := -1; requested <= 1000; requested++ {
		n := NormalizeWorkers(requested, maxHashes)
		if n < workersPerGroup || n > maxWorkers || n%workersPerGroup != 0 {
			t.Errorf("NormalizeWorkers(%d) = %d", requested, n)
		}
	}
	for _, c := range []struct{ hashes, want int }{
		{0, 27}, {1, 27}, {4, 108}, {5, 126}, {6, 126}, {100, 126},
	} {
		if got := NormalizeWorkers(math.MaxInt, c.hashes); got != c.want {
			t.Errorf("NormalizeWorkers(max, %d хешей) = %d, want %d", c.hashes, got, c.want)
		}
	}
	if got := NormalizeWorkers(50, 1); got != 27 {
		t.Errorf("NormalizeWorkers(50, 1) = %d, want 27", got)
	}
}
