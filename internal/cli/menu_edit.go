// menu_edit.go — пункты меню, которые меняют профиль: ссылки на звонки,
// пароль, адрес сервера.
//
// Вопросы — те же, что в мастере (wizard.go): ошибка переспрашивается,
// конец ввода — «ничего не изменилось». В отличие от мастера, пустой ответ на
// пароль и на ссылки, которые нужны непременно, — тоже отмена: Ctrl+D на
// скрытом вводе в терминале не действует.
package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/FreedomCats/csqtt-cli/internal/config"
	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
)

// updateHashes — «Обновить ссылки на звонки VK». Сначала текущие ссылки
// проверяются, как в hashes check: какой звонок закрыт, человек сам не
// узнает. Закрытые убираются, остальные остаются — в том числе те, что
// проверить не удалось: выбросить годную ссылку из-за сбоя сети хуже, чем
// оставить мёртвую.
func (m *menu) updateHashes(ctx context.Context, name string) error {
	path, file, err := m.load()
	if err != nil {
		return err
	}
	p, err := file.Profile(name)
	if err != nil {
		return err
	}

	var current []string
	if raw, err := m.e.secretsFor(path).Get(p.VkHashesRef); err != nil {
		fmt.Fprintf(m.e.stderr, "Текущие ссылки не прочитать: %v\n", err)
	} else {
		current = config.ParseHashes(raw)
	}

	kept := current
	if len(current) > 0 {
		kept = m.checkHashes(ctx, current)
		if m.leaving() {
			return errMenuExit
		}
	}

	w := m.e.stdout
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  1. Добавить новые ссылки (закрытые звонки уберутся)")
	fmt.Fprintln(w, "  2. Заменить все ссылки новыми")
	fmt.Fprintln(w, "  0. Назад")
	choice := 0
	for {
		line, err := m.ask(ctx, "Что сделать? [1]: ")
		if errors.Is(err, errNoInput) {
			return nil
		}
		if err != nil {
			return err
		}
		n, ok := menuNumber(line, 2, 1)
		if ok {
			choice = n
			break
		}
		fmt.Fprintf(m.e.stderr, "Нет такого пункта: %q. Введите 0, 1 или 2.\n", line)
	}
	if choice == 0 {
		fmt.Fprintln(w, "Ссылки не изменены.")
		return nil
	}
	if choice == 2 {
		kept = nil
	}

	// Добавлять можно и ничего: тогда только уберутся закрытые звонки. Не
	// осталось ничего — пустой ответ отменяет пункт.
	empty := linksCancel
	if len(kept) > 0 {
		empty = linksNone
	}
	added, err := m.askCallLinks(ctx, empty, config.MaxHashes-len(kept))
	if errors.Is(err, errWizardCancelled) {
		fmt.Fprintln(w, "Ссылки не изменены.")
		return nil
	}
	if err != nil {
		return err
	}

	hashes := config.ParseHashes(strings.Join(append(slices.Clone(kept), added...), " "))
	if slices.Equal(hashes, current) {
		fmt.Fprintln(w, "Ссылки не изменились.")
		return nil
	}
	return storeProfile(m.e, path, file, p, true, profileSecrets{hashes: hashes})
}

// checkHashes проверяет хеши ядром и печатает исход каждого, как hashes
// check, — номером, без самого хеша. Возвращает те, что стоит оставить: все,
// кроме закрытых звонков. Ctrl+C прерывает проверку, а не меню; не
// проверилось — остаются все.
func (m *menu) checkHashes(ctx context.Context, hashes []string) []string {
	w := m.e.stdout
	corePath, err := m.e.findCore()
	if err != nil {
		fmt.Fprintf(m.e.stderr, "Проверить ссылки не удалось: %v\n", err)
		return hashes
	}
	fmt.Fprintf(w, "Проверяю ссылки на звонки (%d): по одной, обычно несколько секунд на каждую. Прервать — Ctrl+C.\n",
		len(hashes))

	ctx, done := m.connecting(ctx)
	defer done()

	out := newPrinter(w, false)
	out.setHashes(hashes)
	results, err := m.e.checkHashes(ctx, corePath, hashes, out)
	switch {
	case ctx.Err() != nil:
		fmt.Fprintln(m.e.stderr, "Проверка прервана, ссылки остаются как были.")
		return hashes
	case err != nil:
		fmt.Fprintf(m.e.stderr, "Проверить ссылки не удалось: %v\n", err)
		return hashes
	}

	dead := map[int]bool{}
	for i, r := range results {
		n, line := hashCheckLine(out, hashes, i, r)
		switch r.Status {
		case coreproc.HashValid:
		case coreproc.HashInvalid:
			dead[n] = true
			line += "; уберётся"
		default:
			line += "; останется"
		}
		fmt.Fprintln(w, line)
	}

	var kept []string
	for i, h := range hashes {
		if !dead[i+1] {
			kept = append(kept, h)
		}
	}
	switch n := len(hashes) - len(kept); {
	case n == 0:
		fmt.Fprintln(w, "Закрытых звонков нет.")
	case len(kept) == 0:
		fmt.Fprintln(w, "Все звонки закрыты — нужны новые ссылки.")
	default:
		fmt.Fprintf(w, "Закрытых звонков: %d из %d.\n", n, len(hashes))
	}
	return kept
}

func (m *menu) replacePasswordOrPeer(ctx context.Context, name string) error {
	fmt.Fprintln(m.e.stdout, "  1. Пароль подключения")
	fmt.Fprintln(m.e.stdout, "  2. Адрес сервера")
	fmt.Fprintln(m.e.stdout, "  0. Назад")

	for {
		line, err := m.ask(ctx, "Что заменить? [0]: ")
		if errors.Is(err, errNoInput) {
			return nil
		}
		if err != nil {
			return err
		}
		n, ok := menuNumber(line, 2, 0)
		switch {
		case !ok:
			fmt.Fprintf(m.e.stderr, "Нет такого пункта: %q. Введите 0, 1 или 2.\n", line)
			continue
		case n == 1:
			return m.replacePassword(ctx, name)
		case n == 2:
			return m.replacePeer(ctx, name)
		}
		return nil
	}
}

func (m *menu) replacePassword(ctx context.Context, name string) error {
	path, file, err := m.load()
	if err != nil {
		return err
	}
	p, err := file.Profile(name)
	if err != nil {
		return err
	}

	password, err := m.askPassword(ctx, true)
	if errors.Is(err, errWizardCancelled) {
		fmt.Fprintln(m.e.stdout, "Пароль не изменён.")
		return nil
	}
	if err != nil {
		return err
	}
	return storeProfile(m.e, path, file, p, true, profileSecrets{password: password})
}

func (m *menu) replacePeer(ctx context.Context, name string) error {
	path, file, err := m.load()
	if err != nil {
		return err
	}
	p, err := file.Profile(name)
	if err != nil {
		return err
	}

	fmt.Fprintf(m.e.stdout, "Сейчас: %s\n", config.PeerWithPort(p.Peer))
	for {
		line, err := m.wizardAsk(ctx, "Новый адрес сервера [не менять]: ")
		if errors.Is(err, errWizardCancelled) || (err == nil && line == "") {
			fmt.Fprintln(m.e.stdout, "Адрес не изменён.")
			return nil
		}
		if err != nil {
			return err
		}
		peer := config.PeerWithPort(line)
		if err := config.CheckPeer(peer); err != nil {
			fmt.Fprintf(m.e.stderr, "Не похоже на адрес: %v\n", err)
			continue
		}
		p.Peer = peer
		return storeProfile(m.e, path, file, p, true, profileSecrets{})
	}
}
