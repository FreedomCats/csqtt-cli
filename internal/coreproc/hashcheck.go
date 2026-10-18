package coreproc

import (
	"context"
	"fmt"
	"strings"
)

// Состояния HashCheck.Status — как их пишет ядро (main.rs,
// run_vk_hash_validation).
const (
	// HashValid — звонок жив: VK выдал по ссылке анонимный токен и превью.
	HashValid = "valid"
	// HashInvalid — VK ответил ошибкой о звонке (Code): звонок закрыт или
	// ссылки нет.
	HashInvalid = "invalid"
	// HashUnavailable — проверить не вышло: сеть, ответ VK не разобран.
	HashUnavailable = "unavailable"
)

// hashCheckLogLines — сколько последних строк журнала ядра показать, если
// проверка кончилась ошибкой.
const hashCheckLogLines = 5

// HashCheckArgs — аргументы ядра для проверки хешей (--validate-vk-hashes).
// Из Config нужны только хеши — они идут через stdin, как при подключении, —
// и то, чем ядро ходит в VK: Fingerprint и ClientIDs. Сервер не нужен.
func (c Config) HashCheckArgs() []string {
	args := []string{"--secrets-stdin", "--validate-vk-hashes"}
	if c.Fingerprint != "" {
		args = append(args, "--fingerprint="+c.Fingerprint)
	}
	if len(c.ClientIDs) > 0 {
		args = append(args, "--client-ids="+strings.Join(c.ClientIDs, ","))
	}
	return args
}

// CheckHashes проверяет хеши cfg.Hashes в режиме ядра --validate-vk-hashes:
// ядро спрашивает VK о каждом звонке по очереди, печатает HASH_CHECK на каждый
// и выходит. Итоги — в порядке ядра; оно берёт первые шесть хешей.
//
// Хеши идут первой строкой stdin (правка П1, ядро читает её и в этом режиме),
// пароль — пустым: проверке он не нужен, и незачем отдавать его процессу,
// который к серверу не ходит. Журнал ядра отдаётся onLog (nil — молчать).
//
// Команд stdin ядро в этом режиме не читает, поэтому отмена ctx не просит его
// выйти, а сразу завершает процесс.
func CheckHashes(ctx context.Context, path string, cfg Config, onLog func(Log)) ([]HashCheck, error) {
	p, err := start(path, cfg.HashCheckArgs(), nil)
	if err != nil {
		return nil, err
	}
	// Ошибку записи не проверяем, как в Start: она бывает только у вышедшего
	// ядра, и причину скажут журнал и код выхода.
	_ = p.Send(Config{Hashes: cfg.Hashes}.SecretsLine())
	stop := context.AfterFunc(ctx, func() { _ = p.cmd.Process.Kill() })
	defer stop()

	var results []HashCheck
	var lastLog []string
	for m := range p.Messages() {
		switch m := m.(type) {
		case HashCheck:
			results = append(results, m)
		case Log:
			lastLog = append(lastLog, m.Text)
			if len(lastLog) > hashCheckLogLines {
				lastLog = lastLog[1:]
			}
			if onLog != nil {
				onLog(m)
			}
		}
	}
	err = p.Wait()
	switch {
	case ctx.Err() != nil:
		return results, ctx.Err()
	case err != nil:
		tail := ""
		if len(lastLog) > 0 {
			tail = "\n  " + strings.Join(lastLog, "\n  ")
		}
		return results, fmt.Errorf("проверка хешей: ядро завершилось с ошибкой: %w%s", err, tail)
	}
	return results, nil
}
