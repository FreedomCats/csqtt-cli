// hashes.go — команда csqtt-cli hashes: проверка ссылок на звонки VK.
//
// Проверяет ядро в своём режиме --validate-vk-hashes (coreproc.CheckHashes):
// спрашивает VK о каждом звонке так же, как перед подключением, но к серверу
// CSQTT не ходит. Хеши в выводе называются номерами в профиле — сам хеш ключ к
// звонку и не печатается. Профиль проверка не меняет.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/FreedomCats/csqtt-cli/internal/app"
	"github.com/FreedomCats/csqtt-cli/internal/config"
	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
)

func hashesCommand() *command {
	return &command{
		name:    "hashes",
		args:    "check [флаги] [профиль]",
		summary: "проверить звонки VK профиля: живые, мёртвые, недоступные",
		description: `Подкоманда одна — check: ядро спрашивает VK о каждом звонке профиля, к серверу
CSQTT не подключаясь. Итог по каждому хешу — живой, мёртвый (звонок закрыт или
ссылки нет, с кодом ошибки VK) или недоступен (VK не ответил: сеть, лимит).
Хеши называются номерами в профиле, сами хеши не печатаются.

Профиль проверка не меняет: мёртвые хеши заменяют ` + app.Name + ` profile set <профиль> -hashes.
Код возврата — 0, только если живы все.`,
		run: runHashes,
	}
}

func runHashes(ctx context.Context, e *env, args []string) error {
	if len(args) > 0 && isHelpFlag(args[0]) {
		fs := newFlagSet(hashesCommand())
		fs.SetOutput(e.stdout)
		fs.Usage()
		return flag.ErrHelp
	}
	if len(args) == 0 || args[0] != "check" {
		return usagef("нужна подкоманда check: %s hashes check [профиль]", app.Name)
	}
	return runHashesCheck(ctx, e, args[1:])
}

func hashesCheckCommand() *command {
	return &command{
		name:    "hashes check",
		args:    "[флаги] [профиль]",
		summary: "проверить звонки VK профиля",
		run:     runHashesCheck,
	}
}

func runHashesCheck(ctx context.Context, e *env, args []string) error {
	cmd := hashesCheckCommand()
	fs := newFlagSet(cmd)
	configPath := addConfigFlag(fs)
	verbose := addVerboseFlag(fs)
	rest, err := parseWithArgs(e, fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 1 {
		return subUsagef(cmd, "лишний аргумент: профиль называется один (аргумент не печатается)")
	}
	name := ""
	if len(rest) == 1 {
		name = rest[0]
	}

	path, file, err := loadProfiles(*configPath)
	if err != nil {
		return err
	}
	p, err := file.Profile(name)
	if err != nil {
		return err
	}
	raw, err := e.secretsFor(path).Get(p.VkHashesRef)
	if err != nil {
		return fmt.Errorf("хеши профиля %s: %w", p.Name, err)
	}
	hashes := config.ParseHashes(raw)
	if len(hashes) == 0 {
		return fmt.Errorf("в профиле %s нет хешей звонков VK: %s profile set %s -hashes", p.Name, app.Name, p.Name)
	}
	corePath, err := e.findCore()
	if err != nil {
		return err
	}

	// Принтер — для журнала ядра при -v: в нём хеши заменяются номерами.
	out := newPrinter(e.stdout, *verbose)
	out.setHashes(hashes)
	fmt.Fprintf(e.stdout, "Проверка звонков профиля %s: хешей %d, около секунды на каждый.\n", p.Name, len(hashes))

	check := e.hashCheck
	if check == nil {
		check = coreproc.CheckHashes
	}
	results, err := check(ctx, corePath, coreproc.Config{Hashes: hashes}, func(l coreproc.Log) {
		out.message(l)
	})
	switch {
	case err != nil && ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
		// В журнале ядра, который несёт ошибка, могут быть хеши.
		return errors.New(out.mask.Replace(err.Error()))
	}

	alive := 0
	var dead []int
	for i, r := range results {
		n := hashNumber(hashes, r.Hash)
		if n == 0 {
			n = i + 1 // ядро свело хеш к другому виду; порядок у него тот же
		}
		status, detail := hashCheckText(r)
		line := fmt.Sprintf("  №%d  %s", n, status)
		if detail != "" {
			line += " — " + out.mask.Replace(detail)
		}
		fmt.Fprintln(e.stdout, line)
		switch r.Status {
		case coreproc.HashValid:
			alive++
		case coreproc.HashInvalid:
			dead = append(dead, n)
		}
	}

	if len(results) < len(hashes) {
		fmt.Fprintf(e.stdout, "Проверено %d из %d: ядро берёт не больше %d хешей.\n", len(results), len(hashes), config.MaxHashes)
	}
	if len(dead) > 0 {
		fmt.Fprintf(e.stdout, "Мёртвые: %s. Профиль не менялся — заменить хеши: %s profile set %s -hashes\n",
			hashNumbers(dead), app.Name, p.Name)
	}
	if alive < len(hashes) {
		return fmt.Errorf("живых звонков %d из %d", alive, len(hashes))
	}
	fmt.Fprintf(e.stdout, "Все звонки живы (%d).\n", alive)
	return nil
}

// hashCheckText — итог проверки хеша словами и пояснение.
func hashCheckText(r coreproc.HashCheck) (status, detail string) {
	msg := strings.TrimSpace(r.Message)
	switch r.Status {
	case coreproc.HashValid:
		return "живой", ""
	case coreproc.HashInvalid:
		detail = fmt.Sprintf("ошибка VK %d", r.Code)
		if msg != "" {
			detail += ": " + msg
		}
		return "мёртвый", detail
	case coreproc.HashUnavailable:
		return "недоступен", msg
	}
	return r.Status, msg
}
