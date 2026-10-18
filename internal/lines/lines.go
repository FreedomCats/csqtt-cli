// Package lines — общий читатель строк ввода (stdin) для всех, кто ждёт ответа
// человека: вопросов CLI, паузы перед выходом и ручного режима капчи.
//
// Читатель один на процесс. Чтение со stdin нельзя прервать: горутина,
// начавшая Read для капчи, время которой вышло, так и висит на нём, и второй
// читатель того же потока получил бы строки вперемешку с ней. Поэтому
// незаконченное чтение здесь не бросается, а остаётся общим: строку, которую
// оно принесёт, получит следующий, кто спросит.
//
// Читается только по запросу: пока никто не ждёт строки, stdin не трогается.
// На этом стоит скрытый ввод секретов — он читает терминал мимо Reader
// (golang.org/x/term), и ему нельзя, чтобы кто-то читал тот же терминал
// одновременно. Пока Reader не начинал чтения или последнее закончилось
// ответом, такого читателя нет.
package lines

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
)

// Reader — общий читатель строк.
type Reader struct {
	br *bufio.Reader

	mu sync.Mutex
	// cur — чтение строки: идёт или кончилось, но строку ещё никто не забрал.
	// nil — чтения нет.
	cur *read
	// err — ввод кончился (io.EOF) или сломался: дальше строк не будет.
	err error
}

// read — одно чтение строки. line и err пишутся до close(done).
type read struct {
	done chan struct{}
	line string
	err  error
}

// New — читатель строк из r.
func New(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReader(r)}
}

// Read ждёт строку или отмены ctx и возвращает строку без перевода строки.
// Конец ввода — io.EOF: последняя строка без перевода строки ещё отдаётся,
// а следующий вызов получает io.EOF. Отменённый вызов чтения не бросает:
// строку получит следующий вызов (или выбросит DropStale).
func (r *Reader) Read(ctx context.Context) (string, error) {
	for {
		r.mu.Lock()
		if r.err != nil {
			err := r.err
			r.mu.Unlock()
			return "", err
		}
		cur := r.cur
		if cur == nil {
			cur = r.start()
		}
		r.mu.Unlock()

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-cur.done:
		}

		r.mu.Lock()
		if r.cur != cur {
			// Строку забрал другой вызов, пока этот просыпался.
			r.mu.Unlock()
			continue
		}
		r.cur = nil
		if cur.err != nil {
			r.err = cur.err
		}
		r.mu.Unlock()

		line := strings.TrimRight(cur.line, "\r\n")
		if cur.err != nil && (line == "" || !errors.Is(cur.err, io.EOF)) {
			return "", cur.err
		}
		return line, nil
	}
}

// DropStale выбрасывает строку, которую принесло чтение, брошенное прежним
// вызовом: её ввели, когда ответа никто не ждал, — это ответ на прежний
// вопрос. Конец ввода при этом запоминается.
func (r *Reader) DropStale() {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := r.cur
	if cur == nil {
		return
	}
	select {
	case <-cur.done:
		r.cur = nil
		if cur.err != nil {
			r.err = cur.err
		}
	default:
		// Строки ещё нет: её введут уже в ответ на новый вопрос.
	}
}

// start начинает чтение строки. Под mu. Читает одна горутина за раз: новая
// начинается только после того, как прежняя закончила.
func (r *Reader) start() *read {
	cur := &read{done: make(chan struct{})}
	r.cur = cur
	go func() {
		cur.line, cur.err = r.br.ReadString('\n')
		close(cur.done)
	}()
	return cur
}
