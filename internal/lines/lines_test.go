package lines

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestReadLines(t *testing.T) {
	r := New(strings.NewReader("один\r\nдва\nтри"))
	for _, want := range []string{"один", "два", "три"} {
		got, err := r.Read(context.Background())
		if err != nil || got != want {
			t.Fatalf("Read = %q, %v; ожидалось %q", got, err, want)
		}
	}
	for range 2 {
		if _, err := r.Read(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("после конца ввода Read = %v, ожидался io.EOF", err)
		}
	}
}

// Ввод не читается, пока строки никто не просил: скрытый ввод секретов читает
// терминал мимо Reader, и чтение за его спиной забрало бы его строку.
func TestNoReadUntilAsked(t *testing.T) {
	src := &countingReader{r: strings.NewReader("строка\n")}
	r := New(src)
	time.Sleep(20 * time.Millisecond)
	if src.calls != 0 {
		t.Fatalf("ввод прочитан до первого Read: %d раз", src.calls)
	}
	if got, err := r.Read(context.Background()); err != nil || got != "строка" {
		t.Fatalf("Read = %q, %v", got, err)
	}
}

type countingReader struct {
	r     io.Reader
	calls int
}

func (c *countingReader) Read(p []byte) (int, error) {
	c.calls++
	return c.r.Read(p)
}

// Отменённый вызов чтения не бросает: строку получает следующий.
func TestCancelledReadPassesLineOn(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	r := New(pr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Read(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read с отменённым ctx = %v", err)
	}

	got := make(chan string, 1)
	go func() {
		line, _ := r.Read(context.Background())
		got <- line
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := io.WriteString(pw, "ответ\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-got:
		if line != "ответ" {
			t.Fatalf("второй Read = %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("второй Read не получил строки")
	}
}

// Строка, пришедшая, когда её никто не ждал, выбрасывается DropStale; конец
// ввода при этом не теряется.
func TestDropStale(t *testing.T) {
	pr, pw := io.Pipe()
	r := New(pr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = r.Read(ctx)
	if _, err := io.WriteString(pw, "прежний ответ\n"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, r)
	r.DropStale()

	go func() {
		_, _ = io.WriteString(pw, "новый ответ\n")
		pw.Close()
	}()
	if got, err := r.Read(context.Background()); err != nil || got != "новый ответ" {
		t.Fatalf("Read = %q, %v; ожидался новый ответ", got, err)
	}

	// Брошенное чтение упёрлось в конец ввода: DropStale его запоминает.
	_, _ = r.Read(ctx)
	waitDone(t, r)
	r.DropStale()
	if _, err := r.Read(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("после конца ввода Read = %v", err)
	}
}

// DropStale не трогает чтение, которое ещё идёт: его строку введут уже в
// ответ на новый вопрос.
func TestDropStaleKeepsPendingRead(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	r := New(pr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = r.Read(ctx)
	r.DropStale()

	got := make(chan string, 1)
	go func() {
		line, _ := r.Read(context.Background())
		got <- line
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := io.WriteString(pw, "свежий\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-got:
		if line != "свежий" {
			t.Fatalf("Read = %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read не получил строки")
	}
}

// Двое ждут одновременно — строку получает один, второй ждёт следующую.
func TestConcurrentReaders(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	r := New(pr)

	got := make(chan string, 2)
	for range 2 {
		go func() {
			line, err := r.Read(context.Background())
			if err == nil {
				got <- line
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := io.WriteString(pw, "a\nb\n"); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case line := <-got:
			seen[line] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("строки дошли не обе: %v", seen)
		}
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("строки: %v", seen)
	}
}

func TestReadError(t *testing.T) {
	boom := errors.New("сбой")
	r := New(io.MultiReader(strings.NewReader("часть"), errReader{boom}))
	if _, err := r.Read(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Read = %v, ожидалась ошибка чтения", err)
	}
	if _, err := r.Read(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("после ошибки Read = %v", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// waitDone ждёт, пока брошенное чтение закончится.
func waitDone(t *testing.T, r *Reader) {
	t.Helper()
	r.mu.Lock()
	cur := r.cur
	r.mu.Unlock()
	if cur == nil {
		t.Fatal("чтения нет")
	}
	select {
	case <-cur.done:
	case <-time.After(5 * time.Second):
		t.Fatal("чтение не закончилось")
	}
}
