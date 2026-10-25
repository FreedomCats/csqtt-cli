// wizard.go — мастер профиля: новый профиль из ссылки csqtt:// или
// вопросами, по одному полю за раз.
//
// Ошибка в ответе переспрашивается, а не завершает мастер: человек, который
// ошибся в адресе сервера, не обязан заново вводить пароль. Конец ввода на
// любом вопросе — отмена, профиль не записывается.
package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/FreedomCats/csqtt-cli/internal/app"
	"github.com/FreedomCats/csqtt-cli/internal/config"
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
)

// errWizardCancelled — человек закрыл ввод посреди мастера.
var errWizardCancelled = errors.New("мастер профиля отменён")

func (m *menu) create(ctx context.Context, _ string) error {
	path, file, err := m.load()
	if err != nil {
		return err
	}

	err = m.wizard(ctx, path, file)
	if errors.Is(err, errWizardCancelled) {
		fmt.Fprintln(m.e.stdout, "Профиль не записан.")
		return nil
	}
	return err
}

func (m *menu) wizard(ctx context.Context, path string, file *config.File) error {
	w := m.e.stdout
	fmt.Fprintln(w, "Новый профиль. Где в вопросе есть [значение], Enter выбирает его.")
	fmt.Fprintln(w)

	link, err := m.askShareLink(ctx)
	if err != nil {
		return err
	}

	name, existed, err := m.askProfileName(ctx, file, freeProfileName(file))
	if err != nil {
		return err
	}

	// Заменяемый профиль сохраняет остальные поля, и прежде всего device_id:
	// сервер привязывает пароль к устройству, и новый id выглядел бы для него
	// чужим устройством.
	p := &config.Profile{Name: name}
	if existed {
		p = file.Profiles[name]
	}
	// Токен VK от сервера у профиля — только из этой ссылки. Заменяемый
	// профиль с токеном прежней ссылки возвращается к хешам: звонки от
	// аккаунта прежнего сервера новому профилю ни к чему.
	secrets := profileSecrets{dropVKToken: true}
	if link != nil {
		p.Peer = link.Peer
		secrets.password, secrets.hashes, secrets.vkToken = link.Password, link.Hashes, link.VKToken
	} else if p.Peer, err = m.askPeer(ctx); err != nil {
		return err
	}
	switch {
	case secrets.vkToken != "":
		p.VkHashMode = config.VKHashModeAutoJS
	case p.ServerVKToken():
		p.VkHashMode = ""
	}

	// Чего в ссылке не было, спрашивается так же, как при вводе вручную.
	if secrets.password == "" {
		if secrets.password, err = m.askPassword(ctx, false); err != nil {
			return err
		}
	}
	if len(secrets.hashes) == 0 && secrets.vkToken == "" {
		if secrets.hashes, err = m.askCallLinks(ctx, linksRequired, config.MaxHashes); err != nil {
			return err
		}
	}
	if p.Datapath, err = m.askMode(ctx); err != nil {
		return err
	}

	// Первый профиль — профиль по умолчанию. Если профиль уже был один и
	// default_profile не задан, умолчанием он был по правилу «единственный»;
	// второй профиль это правило отменил бы, поэтому умолчание записывается.
	if file.DefaultProfile == "" && !existed {
		switch names := file.Names(); len(names) {
		case 0:
			file.DefaultProfile = name
		case 1:
			file.DefaultProfile = names[0]
		}
	}

	fmt.Fprintln(w)
	if err := storeProfile(m.e, path, file, p, existed, secrets); err != nil {
		return err
	}

	m.current = name
	fmt.Fprintf(w, "Готово. Подключиться к %s — пункт 1 (Enter).\n", name)
	return nil
}

// askShareLink — первый вопрос мастера: ссылка csqtt:// или Enter для ввода
// вручную (nil). Скрыто: в ссылке пароль. Ошибки разбора ссылку не повторяют
// (config.ParseShareLink).
func (m *menu) askShareLink(ctx context.Context) (*config.ShareLink, error) {
	fmt.Fprintln(m.e.stdout, "Если у вас есть ссылка csqtt://… из панели сервера CSQTT, вставьте её —")
	fmt.Fprintf(m.e.stdout, "адрес сервера, пароль и ссылки на звонки %s возьмёт из неё. Нет ссылки — нажмите Enter.\n", app.Name)
	for {
		text, err := m.wizardSecret(ctx, "Ссылка (при вводе не отображается) [ввести вручную]: ")
		if err != nil {
			return nil, err
		}
		if text == "" {
			return nil, nil
		}
		link, err := config.ParseShareLink(text)
		if err != nil {
			fmt.Fprintf(m.e.stderr, "Ссылка не подошла: %v. Вставьте её ещё раз или нажмите Enter.\n", err)
			continue
		}

		fmt.Fprintf(m.e.stdout, "Ссылка принята: сервер %s", link.Peer)
		if link.Password == "" {
			fmt.Fprint(m.e.stdout, ", пароля в ней нет")
		}
		switch n := len(link.Hashes); {
		case link.VKToken != "":
			fmt.Fprint(m.e.stdout, ", звонки VK создаются от аккаунта, выданного сервером")
		case n > 0:
			fmt.Fprintf(m.e.stdout, ", ссылок на звонки: %d", n)
		default:
			fmt.Fprint(m.e.stdout, ", ссылок на звонки в ней нет")
		}
		fmt.Fprintln(m.e.stdout, ".")
		return link, nil
	}
}

// wizardAsk — вопрос мастера обычной строкой. Конец ввода — отмена.
func (m *menu) wizardAsk(ctx context.Context, prompt string) (string, error) {
	line, err := m.ask(ctx, prompt)
	if errors.Is(err, errNoInput) {
		return "", errWizardCancelled
	}
	return line, err
}

// wizardSecret — то же скрытым вводом.
func (m *menu) wizardSecret(ctx context.Context, prompt string) (string, error) {
	value, err := m.e.askSecret(ctx, prompt)
	if errors.Is(err, errNoInput) {
		return "", errWizardCancelled
	}
	return value, err
}

// askProfileName спрашивает имя профиля. Имя занято — предлагает заменить
// тот профиль; existed — согласились заменить.
func (m *menu) askProfileName(ctx context.Context, file *config.File, suggested string) (name string, existed bool, err error) {
	for {
		name, err := m.wizardAsk(ctx, fmt.Sprintf("Имя профиля [%s]: ", suggested))
		if err != nil {
			return "", false, err
		}
		if name == "" {
			name = suggested
		}
		if err := config.CheckProfileName(name); err != nil {
			fmt.Fprintf(m.e.stderr, "Такое имя не подойдёт: латинские буквы, цифры, дефис и подчёркивание, например %s.\n",
				freeProfileName(file))
			continue
		}
		if _, taken := file.Profiles[name]; !taken {
			return name, false, nil
		}

		replace, err := m.e.confirm(ctx, fmt.Sprintf("Профиль %s уже есть. Заменить его?", name))
		if err != nil {
			return "", false, err
		}
		if replace {
			return name, true, nil
		}
		fmt.Fprintln(m.e.stderr, "Назовите новый профиль иначе.")
	}
}

// freeProfileName — имя, которое мастер предлагает: home, если свободно, иначе
// первое свободное profile-N.
func freeProfileName(file *config.File) string {
	if _, taken := file.Profiles["home"]; !taken {
		return "home"
	}
	for n := 2; ; n++ {
		name := "profile-" + strconv.Itoa(n)
		if _, taken := file.Profiles[name]; !taken {
			return name
		}
	}
}

func (m *menu) askPeer(ctx context.Context) (string, error) {
	for {
		line, err := m.wizardAsk(ctx, "Адрес сервера CSQTT (например, example.org или example.org:"+config.DefaultPeerPort+"): ")
		if err != nil {
			return "", err
		}
		if line == "" {
			fmt.Fprintln(m.e.stderr, "Адрес сервера нужен: его даёт тот, кто выдал вам пароль.")
			continue
		}
		// Без порта — порт сервера CSQTT по умолчанию. В профиль он
		// записывается явно: так видно, куда профиль подключается.
		peer := config.PeerWithPort(line)
		if err := config.CheckPeer(peer); err != nil {
			fmt.Fprintf(m.e.stderr, "Не похоже на адрес: %v\n", err)
			continue
		}
		if peer != line {
			fmt.Fprintf(m.e.stdout, "Порт не указан — взят %s: %s\n", config.DefaultPeerPort, peer)
		}
		return peer, nil
	}
}

// askPassword спрашивает пароль дважды: опечатку в невидимом вводе иначе не
// заметить, а обнаружилась бы она отказом сервера при подключении.
// cancellable — вопрос из пункта правки: пустой ответ отменяет пункт
// (errWizardCancelled). Иначе из скрытого ввода не выйти: Ctrl+D в терминале
// он не замечает (x/term), а Ctrl+C закрывает всё меню. В мастере пустой
// ответ переспрашивается: там отмена — конец ввода на обычном вопросе.
func (m *menu) askPassword(ctx context.Context, cancellable bool) (string, error) {
	prompt := "Пароль подключения (при вводе не отображается): "
	if cancellable {
		prompt = "Новый пароль подключения (при вводе не отображается) [не менять]: "
	}
	for {
		value, err := m.wizardSecret(ctx, prompt)
		if err != nil {
			return "", err
		}
		switch {
		case value == "" && cancellable:
			return "", errWizardCancelled
		case value == "":
			fmt.Fprintln(m.e.stderr, "Пароль не может быть пустым.")
			continue
		}
		repeat, err := m.wizardSecret(ctx, "Повторите пароль: ")
		if err != nil {
			return "", err
		}
		if repeat != value {
			fmt.Fprintln(m.e.stderr, "Пароли не совпали. Введите ещё раз.")
			continue
		}
		return value, nil
	}
}

// linksEmpty — что значит пустой ответ на вопрос о ссылках на звонки.
type linksEmpty int

const (
	// linksRequired — переспросить: мастер, отмена там — конец ввода.
	linksRequired linksEmpty = iota
	// linksCancel — отменить пункт правки (errWizardCancelled); почему не
	// переспрашивать — см. askPassword.
	linksCancel
	// linksNone — новых ссылок нет: к оставшимся ничего не добавляется.
	linksNone
)

// askCallLinks спрашивает ссылки на звонки VK и разбирает их так же, как
// ядро (config.ParseHashes). Скрыто: по ссылке в звонок зайдёт кто угодно,
// это такой же секрет, как пароль. empty — что значит пустой ответ; room —
// сколько ссылок ещё поместится: ядро берёт не больше config.MaxHashes.
func (m *menu) askCallLinks(ctx context.Context, empty linksEmpty, room int) ([]string, error) {
	prompt := "Ссылки на звонки VK через пробел (при вводе не отображаются): "
	switch empty {
	case linksCancel:
		prompt = "Новые ссылки на звонки VK через пробел (не отображаются) [отмена]: "
	case linksNone:
		prompt = "Новые ссылки на звонки VK через пробел (не отображаются) [без новых]: "
	}
	for {
		value, err := m.wizardSecret(ctx, prompt)
		if err != nil {
			return nil, err
		}
		switch {
		case value == "" && empty == linksCancel:
			return nil, errWizardCancelled
		case value == "" && empty == linksNone:
			return nil, nil
		}
		hashes := config.ParseHashes(value)
		switch n := len(hashes); {
		case n == 0:
			fmt.Fprintln(m.e.stderr, "Не нашлось ни одной ссылки. Ссылка на звонок выглядит так: https://vk.com/call/join/…")
			continue
		case n > room:
			fmt.Fprintf(m.e.stderr, "Ссылок %d, а поместится %d: ядро берёт не больше %d звонков. Введите меньше.\n",
				n, room, config.MaxHashes)
			continue
		}
		fmt.Fprintf(m.e.stdout, "Принято ссылок: %d\n", len(hashes))
		return hashes, nil
	}
}

// askMode спрашивает режим. Весь трафик — первым и по Enter: человеку без
// технической подготовки прокси настраивать негде. Где tun не работает, по
// Enter — socks, а о tun сказано, почему его нет; узнаётся это у datapath,
// как и в connect (tunRequirementsError). Где tun работает только без защиты
// DNS и kill-switch (на Windows — до неё), меню его тоже не предлагает:
// согласие на утечку — флаг -allow-leak командной строки, а не Enter.
func (m *menu) askMode(ctx context.Context) (string, error) {
	w := m.e.stdout
	socks := fmt.Sprintf("Только программы, в которых указан прокси SOCKS5 %s.", datapath.DefaultSocksListen)
	fmt.Fprintln(w, "Что пускать через туннель?")

	req := newTun(datapath.TunOptions{}).Requires()
	unsupported := req.Unsupported
	if unsupported == nil && req.DNSGuard && tunGuardMissing != nil {
		unsupported = fmt.Errorf("%w (подключиться так можно только из командной строки: %s connect -datapath tun -allow-leak)",
			tunGuardMissing, app.Name)
	}
	if unsupported != nil {
		fmt.Fprintf(w, "  1. %s\n", socks)
		fmt.Fprintf(w, "Весь трафик компьютера (режим tun) здесь недоступен: %v.\n", unsupported)
		for {
			line, err := m.wizardAsk(ctx, "Выберите [1]: ")
			if err != nil {
				return "", err
			}
			if n, ok := menuNumber(line, 1, 1); ok && n == 1 {
				return config.DatapathSocks, nil
			}
			fmt.Fprintf(m.e.stderr, "Нет такого пункта: %q. Введите 1.\n", line)
		}
	}

	fmt.Fprintln(w, "  1. Весь трафик компьютера. "+adminNote)
	fmt.Fprintf(w, "  2. %s\n", socks)
	for {
		line, err := m.wizardAsk(ctx, "Выберите [1]: ")
		if err != nil {
			return "", err
		}
		switch n, ok := menuNumber(line, 2, 1); {
		case ok && n == 1:
			return config.DatapathTun, nil
		case ok && n == 2:
			return config.DatapathSocks, nil
		}
		fmt.Fprintf(m.e.stderr, "Нет такого пункта: %q. Введите 1 или 2.\n", line)
	}
}
