// policy.go — политика перезапуска: задержки между попытками и годность хешей.
//
// Написана заново по §09 архитектуры: в qwdtt-cli этот слой (restart.go)
// перенесён из Android-клиента qWDTT под GPL-3 и сюда не идёт (§11).
package supervisor

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Задержки между попытками: 1 · 2^n секунд, где n — сколько попыток подряд
// кончились неудачей, но не больше maxBackoff.
const (
	baseBackoff = time.Second
	maxBackoff  = 64 * time.Second
)

// ServerRestartDelay — пауза после SERVER_RESTART: сервер перезапускают из
// панели, и сразу стучаться незачем. Столько же ждёт Android-клиент.
const ServerRestartDelay = 4 * time.Second

// backoff считает задержку перед следующей попыткой.
type backoff struct{ failures int }

// next — задержка после ещё одной неудачи подряд.
func (b *backoff) next() time.Duration {
	d := baseBackoff << min(b.failures, 6) // 64 с — это 1 << 6
	b.failures++
	return min(d, maxBackoff)
}

// reset — туннель поднялся: прошлые неудачи больше не в счёт.
func (b *backoff) reset() { b.failures = 0 }

// Повторы временных ошибок API VK в auto_js (§08а): до PROCESS ядро открывает
// сессию аккаунта и создаёт звонок, и на коды, которые проходят сами
// (coreproc.FatalError.Transient), попытка повторяется — но не больше
// vkRetryLimit раз подряд, с паузами 2 · 2^k секунд: 2, 4, 8, 16, 32. Паузы
// длиннее общего backoff и повторов конечное число: каждая попытка — новые
// сессии и запросы под аккаунтом.
const (
	vkRetryLimit   = 5
	vkRetryBackoff = 2 * time.Second
)

// vkRetries считает временные ошибки VK подряд.
type vkRetries struct{ failures int }

// next — ещё одна временная ошибка: пауза перед повтором; ok = false —
// повторы исчерпаны.
func (r *vkRetries) next() (delay time.Duration, ok bool) {
	if r.failures >= vkRetryLimit {
		return 0, false
	}
	d := vkRetryBackoff << r.failures
	r.failures++
	return d, true
}

// reset — туннель заработал: VK отвечает, счёт заново.
func (r *vkRetries) reset() { r.failures = 0 }

// DeadHash — хеш, исключённый из попыток: звонок недоступен.
type DeadHash struct {
	Hash string
	// Code — код ошибки VK из CALL_UNAVAILABLE.
	Code int64
}

// HashPool — хеши звонков профиля и то, какие из них ещё годны.
//
// Исключает хеш только CALL_UNAVAILABLE: о прочих отказах ядро не говорит,
// какой хеш виноват, а сеть или сервер подводят все хеши разом.
type HashPool struct {
	mu     sync.Mutex
	hashes []string
	dead   []DeadHash
}

// NewHashPool — пул из хешей профиля; повторы отбрасываются, порядок
// сохраняется.
func NewHashPool(hashes []string) *HashPool {
	p := &HashPool{}
	for _, h := range hashes {
		if h != "" && !slices.Contains(p.hashes, h) {
			p.hashes = append(p.hashes, h)
		}
	}
	return p
}

// Alive — годные хеши в исходном порядке: их получит следующая попытка.
func (p *HashPool) Alive() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	alive := make([]string, 0, len(p.hashes))
	for _, h := range p.hashes {
		if !p.isDead(h) {
			alive = append(alive, h)
		}
	}
	return alive
}

// Dead — исключённые хеши в порядке исключения.
func (p *HashPool) Dead() []DeadHash {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.dead)
}

// Exclude исключает хеш. false — хеш не из пула или уже исключён: ядро шлёт
// CALL_UNAVAILABLE по разу на хеш, но новая попытка — новое ядро.
func (p *HashPool) Exclude(hash string, code int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.Contains(p.hashes, hash) || p.isDead(hash) {
		return false
	}
	p.dead = append(p.dead, DeadHash{Hash: hash, Code: code})
	return true
}

// Exhausted — годных хешей не осталось.
func (p *HashPool) Exhausted() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.dead) == len(p.hashes)
}

func (p *HashPool) isDead(hash string) bool {
	return slices.ContainsFunc(p.dead, func(d DeadHash) bool { return d.Hash == hash })
}

// HashesExhaustedError — годных хешей не осталось: туннель остановлен.
type HashesExhaustedError struct {
	Dead []DeadHash
}

// Error говорит, что делать дальше. Хешей в тексте нет: сообщение уходит и в
// журнал, а хеш — ключ к звонку.
func (e *HashesExhaustedError) Error() string {
	codes := make([]string, 0, len(e.Dead))
	for _, d := range e.Dead {
		if c := strconv.FormatInt(d.Code, 10); !slices.Contains(codes, c) {
			codes = append(codes, c)
		}
	}
	what := "звонок VK недоступен"
	if len(e.Dead) > 1 {
		what = fmt.Sprintf("все звонки VK недоступны (%d)", len(e.Dead))
	}
	return fmt.Sprintf("%s, ошибка VK %s: нужны новые ссылки на звонки — "+
		"создайте звонок в VK и запишите ссылку в профиль (csqtt-cli profile set)",
		what, strings.Join(codes, ", "))
}
