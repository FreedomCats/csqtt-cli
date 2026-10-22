// vk.go — команда csqtt-cli vk: вход в аккаунт VK для режима auto_js
// (§08а doc/architecture.md).
//
// Вход один на установку, а не на профиль: токен лежит в хранилище секретов,
// как пароль подключения, а в profiles.toml — секция [vk] со ссылкой на него
// и id аккаунта. Сам токен не печатается никогда — ни в выводе, ни в журнале,
// ни в сообщениях об ошибках.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/FreedomCats/csqtt-cli/internal/app"
	"github.com/FreedomCats/csqtt-cli/internal/captcha"
	"github.com/FreedomCats/csqtt-cli/internal/config"
)

func vkCommand() *command {
	return &command{
		name:    "vk",
		args:    "<подкоманда> [флаги]",
		summary: "вход в аккаунт VK для режима auto_js: login, logout, status",
		description: `Подкоманды:
  login     войти в VK в окне браузера и сохранить токен
  logout    стереть токен с этой машины
  status    вошли ли в VK и каким аккаунтом; токен не печатается

В режиме auto_js ядро само создаёт звонки VK от имени вашего аккаунта, и
ссылки на звонки не нужны. Для этого нужен вход в VK: страница входа VK
открывается в окне браузера, и вы входите в неё сами. Вход один на все
профили. Перед входом ` + app.Name + ` объясняет, чем рискует аккаунт и какой доступ
получает токен, и спрашивает согласие.

Подключаться в этом режиме: ` + app.Name + ` profile set <профиль> -vk-hash-mode auto_js,
затем ` + app.Name + ` connect.

Флаги у каждой подкоманды свои: ` + app.Name + ` vk login -h.`,
		run: runVK,
	}
}

// vkSubcommands — подкоманды в порядке помощи; имена полные, как у profile.
func vkSubcommands() []*command {
	return []*command{vkLoginCommand(), vkLogoutCommand(), vkStatusCommand()}
}

func runVK(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return usagef("не названа подкоманда; бывают: login, logout, status")
	}
	if isHelpFlag(args[0]) {
		fs := newFlagSet(vkCommand())
		fs.SetOutput(e.stdout)
		fs.Usage()
		return flag.ErrHelp
	}
	if strings.HasPrefix(args[0], "-") {
		return usagef("флаги ставятся после подкоманды: %s vk <подкоманда> [флаги]", app.Name)
	}
	for _, sub := range vkSubcommands() {
		if sub.name == "vk "+args[0] {
			return sub.run(ctx, e, args[1:])
		}
	}
	return usagef("неизвестная подкоманда %q; бывают: login, logout, status", args[0])
}

// vkRisk — предупреждение перед входом. Согласие на него спрашивается при
// каждом входе: вход редкий, а условия важные.
const vkRisk = `Вход в VK для режима auto_js. Прочитайте перед входом:

  - Режим пользуется неофициальными браузерными методами VK: ядро создаёт
    звонок и получает доступ к нему так, как это делает браузер, и маскирует
    запросы под браузер. Блокировку аккаунта VK исключить нельзя, и насколько
    она вероятна, неизвестно. Если основной аккаунт вам дорог, входите
    второстепенным.
  - Токен, который VK выдаст, бессрочный и даёт широкий доступ к аккаунту:
    все права, кроме сообщений. ` + app.Name + ` хранит его как пароль — в хранилище
    секретов.
  - Каждое подключение в режиме auto_js создаёт от имени аккаунта звонок и
    открывает под аккаунтом сессии звонков VK; при переподключениях их число
    не ограничено.

Согласие здесь — это и согласие на доступ токена к аккаунту: при входе через
VK ID экрана с правами приложения VK может не показать вовсе, окно просто
закроется после входа.

Дальше откроется окно браузера со страницей входа VK. Войдите в нём. Если VK
покажет экран доступа приложения, подтвердите его сами — ` + app.Name + ` в окне
ничего не нажимает за вас.
`

// --- vk login ---

// loginTimeoutText — срок входа словами.
var loginTimeoutText = fmt.Sprintf("%d мин", int(captcha.LoginTimeout.Minutes()))

func vkLoginCommand() *command {
	return &command{
		name:    "vk login",
		args:    "[флаги]",
		summary: "войти в VK в окне браузера и сохранить токен",
		description: `Перед входом — предупреждение о риске для аккаунта и вопрос о согласии;
без согласия окно не открывается. Окно — браузер на Chromium (Linux) или
WebView2 (Windows) со своим пустым профилем: после входа он стирается вместе с
cookie VK. Токен ложится в хранилище секретов, как пароль подключения;
прежний вход заменяется. На вход — ` + loginTimeoutText + `.

Нужен терминал: согласие спрашивается у человека.`,
		run: runVKLogin,
	}
}

func runVKLogin(ctx context.Context, e *env, args []string) error {
	cmd := vkLoginCommand()
	fs := newFlagSet(cmd)
	configPath := addConfigFlag(fs)
	verbose := fs.Bool("v", false, "подробно: что видит окно входа (без адресов и токена)")
	rest, err := parseWithArgs(e, fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return subUsagef(cmd, "лишний аргумент: вход один на все профили, имя профиля не нужно (аргумент не печатается)")
	}
	if !e.stdinTerminal() {
		return errors.New("вход в VK спрашивает согласие у человека — запустите команду в терминале")
	}

	// Файл настроек читается до окна: испорченный файл лучше узнать до входа.
	// Записывается же копия, прочитанная после окна (ниже).
	if _, _, err := loadProfilesForWrite(*configPath); err != nil {
		return err
	}

	fmt.Fprint(e.stderr, "\n"+vkRisk+"\n")
	agreed, err := e.confirm(ctx, "Войти в VK на этих условиях?")
	if err != nil {
		return err
	}
	if !agreed {
		fmt.Fprintln(e.stdout, "Отменено: вход не выполнен, ничего не изменилось.")
		return nil
	}

	opts := captcha.LoginOptions{Out: e.stderr}
	if *verbose {
		opts.Logf = func(format string, args ...any) { fmt.Fprintf(e.stderr, format+"\n", args...) }
	}
	window, err := e.loginWindow(opts)
	if err != nil {
		return fmt.Errorf("окна для входа нет: %w; нужен браузер на Chromium (Linux) или WebView2 Runtime (Windows)", err)
	}

	fmt.Fprintf(e.stderr, "Время на вход — %s; отменить — Ctrl+C.\n", loginTimeoutText)
	loginCtx, cancel := context.WithTimeout(ctx, captcha.LoginTimeout)
	defer cancel()
	tok, err := window.Login(loginCtx)
	switch {
	case err != nil && ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("время на вход (%s) вышло: токен не записан", loginTimeoutText)
	case err != nil:
		return fmt.Errorf("%w; токен не записан", err)
	case strings.TrimSpace(tok.AccessToken) == "":
		return errors.New("окно входа вернулось без токена; токен не записан")
	}

	// Окно было открыто до пяти минут, и файл за это время могли поменять:
	// profile add в другом терминале, connect дописал device_id. Копия,
	// прочитанная до окна, затёрла бы эти изменения.
	path, file, err := loadProfilesForWrite(*configPath)
	if err != nil {
		return fmt.Errorf("%w; токен не записан", err)
	}
	store := e.secretsFor(path)
	ref, err := store.Put(config.VKTokenSecret, tok.AccessToken)
	if err != nil {
		return fmt.Errorf("токен VK не записан: %w", err)
	}
	old := file.VK
	file.VK = config.VKSection{TokenRef: ref, UserID: tok.UserID}
	if err := config.Save(path, file); err != nil {
		// Без ссылки в файле токен в хранилище никому не нужен.
		if old.TokenRef != ref {
			_ = store.Delete(ref)
		}
		return err
	}
	// Прежний токен мог лежать под другой ссылкой (другое хранилище).
	if strings.TrimSpace(old.TokenRef) != "" && old.TokenRef != ref {
		if err := store.Delete(old.TokenRef); err != nil {
			fmt.Fprintf(e.stderr, "Предупреждение: прежний токен %s не удалён: %v\n", old.TokenRef, err)
		}
	}

	fmt.Fprintf(e.stdout, "Вход в VK выполнен: аккаунт %s.\n", userIDText(tok.UserID))
	fmt.Fprintf(e.stdout, "Токен записан: %s; ссылка на него — в %s.\n", config.SecretRefKind(ref), path)
	if old.LoggedIn() {
		fmt.Fprintln(e.stdout, "Прежний вход заменён.")
	}
	fmt.Fprintf(e.stdout, "Стереть токен с этой машины: %s vk logout\n", app.Name)
	return nil
}

// loginWindow — окно входа VK: подменённое тестом или окно этой системы.
func (e *env) loginWindow(opts captcha.LoginOptions) (captcha.LoginWindow, error) {
	if e.vkLogin != nil {
		return e.vkLogin(opts)
	}
	return captcha.NewLoginWindow(opts)
}

// stdinTerminal — stdin — терминал: спросить есть у кого. Тесты подменяют
// проверку через env.tty.
func (e *env) stdinTerminal() bool {
	if e.tty != nil {
		return e.tty()
	}
	_, ok := e.terminalFD()
	return ok
}

func userIDText(id string) string {
	if id == "" {
		return "id VK не прислал"
	}
	return "id " + id
}

// --- vk logout ---

func vkLogoutCommand() *command {
	return &command{
		name:    "vk logout",
		args:    "[флаги]",
		summary: "стереть токен VK с этой машины",
		description: `Стирает токен из хранилища секретов и секцию [vk] из profiles.toml. К VK
команда не ходит: у VK токен остаётся действительным, пока вы не отзовёте
доступ приложения в настройках аккаунта VK.`,
		run: runVKLogout,
	}
}

func runVKLogout(_ context.Context, e *env, args []string) error {
	cmd := vkLogoutCommand()
	fs := newFlagSet(cmd)
	configPath := addConfigFlag(fs)
	rest, err := parseWithArgs(e, fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return subUsagef(cmd, "лишний аргумент: logout аргументов не принимает (аргумент не печатается)")
	}

	path, file, err := loadProfilesForWrite(*configPath)
	if err != nil {
		return err
	}
	if file.VK == (config.VKSection{}) {
		fmt.Fprintln(e.stdout, "Входа в VK нет: стирать нечего.")
		return nil
	}

	if ref := strings.TrimSpace(file.VK.TokenRef); ref != "" {
		if err := e.secretsFor(path).Delete(ref); err != nil {
			// Как у profile rm: секрет мог пропасть вручную или остаться от
			// другой системы, а ссылку из файла убрать надо всё равно.
			fmt.Fprintf(e.stderr, "Предупреждение: токен %s не удалён из хранилища: %v\n", ref, err)
		}
	}
	file.VK = config.VKSection{}
	if err := config.Save(path, file); err != nil {
		return err
	}

	fmt.Fprintln(e.stdout, "Токен VK стёрт с этой машины.")
	fmt.Fprintln(e.stdout, "У VK он остаётся действительным, пока доступ приложения не отозван: это делается в")
	fmt.Fprintln(e.stdout, "настройках аккаунта VK, в списке приложений и сайтов, которым дан доступ.")
	return nil
}

// --- vk status ---

func vkStatusCommand() *command {
	return &command{
		name:    "vk status",
		args:    "[флаги]",
		summary: "вошли ли в VK и каким аккаунтом",
		description: `Показывает, есть ли вход в VK, id аккаунта и где лежит токен — читается ли
он на этой машине. Сам токен не печатается.`,
		run: runVKStatus,
	}
}

func runVKStatus(_ context.Context, e *env, args []string) error {
	cmd := vkStatusCommand()
	fs := newFlagSet(cmd)
	configPath := addConfigFlag(fs)
	rest, err := parseWithArgs(e, fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return subUsagef(cmd, "лишний аргумент: status аргументов не принимает (аргумент не печатается)")
	}

	path, file, err := loadProfilesForWrite(*configPath)
	if err != nil {
		return err
	}
	if !file.VK.LoggedIn() {
		fmt.Fprintf(e.stdout, "Вход в VK не выполнен. Войти: %s vk login\n", app.Name)
		return nil
	}

	fmt.Fprintln(e.stdout, "Вход в VK выполнен.")
	userID := file.VK.UserID
	if userID == "" {
		userID = "не известен"
	}
	printField(e.stdout, "user_id", userID, "")
	printField(e.stdout, "token", tokenNote(e.secretsFor(path), file.VK.TokenRef), "")
	return nil
}

// tokenNote — читается ли токен и где он лежит. Значения нет: оно читается
// только чтобы убедиться, что оно есть.
func tokenNote(store config.SecretStore, ref string) string {
	value, err := store.Get(ref)
	switch {
	case err != nil:
		return fmt.Sprintf("не читается: %v", err)
	case strings.TrimSpace(value) == "":
		return "пустой — войдите заново"
	}
	return fmt.Sprintf("есть (%s)", config.SecretRefKind(ref))
}
