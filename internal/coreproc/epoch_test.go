package coreproc

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// epochSalt — какой соль должна быть: 16 случайных байт в hex.
var epochSalt = regexp.MustCompile(`^[0-9a-f]{32}$`)

// epochArgs достаёт из аргументов ядра эпоху и проверяет её вид: номер —
// десятичное число больше нуля, соль — epochSalt; каждый флаг ровно один раз.
func epochArgs(args []string) (gen, salt string, err error) {
	var gens, salts []string
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--gen="); ok {
			gens = append(gens, v)
		}
		if v, ok := strings.CutPrefix(a, "--salt="); ok {
			salts = append(salts, v)
		}
	}
	if len(gens) != 1 || len(salts) != 1 {
		return "", "", errors.New("нужно по одному --gen и --salt: " + strings.Join(args, " "))
	}
	if n, err := strconv.ParseUint(gens[0], 10, 64); err != nil || n == 0 || strconv.FormatUint(n, 10) != gens[0] {
		return "", "", errors.New("--gen=" + gens[0])
	}
	if !epochSalt.MatchString(salts[0]) {
		return "", "", errors.New("--salt=" + salts[0])
	}
	return gens[0], salts[0], nil
}

func TestNewEpoch(t *testing.T) {
	var prev epoch
	salts := map[string]bool{}
	for range 100 {
		e := newEpoch()
		if e.gen <= prev.gen {
			t.Fatalf("номер %d после %d", e.gen, prev.gen)
		}
		if !epochSalt.MatchString(e.salt) || salts[e.salt] {
			t.Fatalf("соль %q: не того вида или повтор", e.salt)
		}
		salts[e.salt] = true
		prev = e
	}
}

func TestArgsEpoch(t *testing.T) {
	cfg := Config{Peer: "203.0.113.10:46000"}
	if args := cfg.Args(); slices.ContainsFunc(args, func(a string) bool {
		return strings.HasPrefix(a, "--gen=") || strings.HasPrefix(a, "--salt=")
	}) {
		t.Errorf("Args() без эпохи = %q", args)
	}
	cfg.epoch = epoch{gen: 1234567890123, salt: "00112233445566778899aabbccddeeff"}
	gen, salt, err := epochArgs(cfg.Args())
	if err != nil || gen != "1234567890123" || salt != cfg.epoch.salt {
		t.Errorf("эпоха в Args() = %q, %q, %v", gen, salt, err)
	}
	// Проверке хешей сервер не нужен — и эпоха тоже.
	if args := cfg.HashCheckArgs(); slices.ContainsFunc(args, func(a string) bool {
		return strings.HasPrefix(a, "--gen=") || strings.HasPrefix(a, "--salt=")
	}) {
		t.Errorf("HashCheckArgs() = %q", args)
	}
}

// Каждый запуск ядра — со своей эпохой, хотя Config один и эпохи в нём нет.
func TestStartNewEpoch(t *testing.T) {
	type pair struct{ gen, salt string }
	var got []pair
	for range 2 {
		p, err := startConfig(os.Args[0], fakeConfig, []string{fakeCoreEnv + "=epoch"})
		if err != nil {
			t.Fatal(err)
		}
		var line string
		for m := range p.Messages() {
			if l, ok := m.(Log); ok {
				line = l.Text
			}
		}
		if err := p.Wait(); err != nil {
			t.Fatalf("ядро: %v, журнал %q", err, line)
		}
		f := strings.Fields(line)
		if len(f) != 3 || f[0] != "EPOCH" {
			t.Fatalf("журнал ядра = %q", line)
		}
		got = append(got, pair{f[1], f[2]})
	}
	if got[0].gen == got[1].gen || got[0].salt == got[1].salt {
		t.Errorf("эпохи двух запусков = %v", got)
	}
}
