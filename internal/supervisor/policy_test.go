package supervisor

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBackoffSequence(t *testing.T) {
	var b backoff
	var got []time.Duration
	for range 9 {
		got = append(got, b.next())
	}
	want := []time.Duration{1, 2, 4, 8, 16, 32, 64, 64, 64}
	for i := range want {
		want[i] *= time.Second
	}
	if !slices.Equal(got, want) {
		t.Errorf("задержки %v, ожидались %v", got, want)
	}
	b.reset()
	if d := b.next(); d != time.Second {
		t.Errorf("после reset — %v", d)
	}
}

// Счётчик неудач не переполняет сдвиг, сколько бы их ни было.
func TestBackoffLongStreak(t *testing.T) {
	b := backoff{failures: 1 << 20}
	if d := b.next(); d != maxBackoff {
		t.Errorf("задержка %v", d)
	}
}

func TestHashPool(t *testing.T) {
	p := NewHashPool([]string{"a", "b", "", "a", "c"})
	if got := p.Alive(); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("Alive() = %v", got)
	}
	if p.Exclude("x", 951) {
		t.Error("исключён хеш не из пула")
	}
	if !p.Exclude("b", 951) || p.Exclude("b", 954) {
		t.Error("повторное исключение")
	}
	if !p.Exclude("a", 954) {
		t.Error("не исключён")
	}
	if got := p.Alive(); !slices.Equal(got, []string{"c"}) || p.Exhausted() {
		t.Errorf("Alive() = %v", got)
	}
	if got := p.Dead(); !slices.Equal(got, []DeadHash{{"b", 951}, {"a", 954}}) {
		t.Errorf("Dead() = %v", got)
	}
	p.Exclude("c", 951)
	if !p.Exhausted() || len(p.Alive()) != 0 {
		t.Error("пул не исчерпан")
	}
}

func TestHashesExhaustedError(t *testing.T) {
	one := (&HashesExhaustedError{Dead: []DeadHash{{"secret-hash", 951}}}).Error()
	if !strings.HasPrefix(one, "звонок VK недоступен, ошибка VK 951:") || strings.Contains(one, "secret-hash") {
		t.Errorf("один хеш: %s", one)
	}
	many := (&HashesExhaustedError{Dead: []DeadHash{{"a", 951}, {"b", 951}, {"c", 954}}}).Error()
	if !strings.HasPrefix(many, "все звонки VK недоступны (3), ошибка VK 951, 954:") {
		t.Errorf("несколько: %s", many)
	}
}

func TestServerErrorText(t *testing.T) {
	cases := []struct {
		err  ServerError
		want string
	}{
		{ServerError{"handshake_rejected", "FATAL_AUTH: срок действия пароля истёк"},
			"сервер CSQTT отказал в подключении: срок действия пароля истёк"},
		{ServerError{"handshake_timeout", "FATAL_HANDSHAKE: сервер не подтвердил конфигурацию"},
			"сервер не подтвердил конфигурацию"},
		{ServerError{"other", "что-то: случилось"}, "что-то: случилось"},
	}
	for _, tt := range cases {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("%q, ожидался %q", got, tt.want)
		}
	}
}
