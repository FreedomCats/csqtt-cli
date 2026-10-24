// profile.go — команда csqtt-cli profile: профили подключения и их секреты.
//
// Пять подкоманд со своими флагами, поэтому у самой profile флагов нет:
// они ставятся после подкоманды, как в `csqtt-cli profile set home -peer …`.
//
// Пароль подключения и хеши звонков VK — только скрытым вводом и только в
// хранилище секретов: ни в аргументах, ни в выводе, ни в сообщениях об
// ошибках их нет. В profiles.toml уходят ссылки на них.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/FreedomCats/csqtt-cli/internal/app"
	"github.com/FreedomCats/csqtt-cli/internal/config"
	"github.com/FreedomCats/csqtt-cli/internal/coreproc"
	"github.com/FreedomCats/csqtt-cli/internal/datapath"
)

func profileCommand() *command {
	return &command{
		name:    "profile",
		args:    "<подкоманда> [имя] [флаги]",
		summary: "профили подключения: add, set, show, rm, list",
		description: `Подкоманды:
  add <имя>     завести профиль: из ссылки csqtt:// (-link) или по полям (-peer)
  set <имя>     изменить профиль; -password и -hashes спрашивают секреты заново
  show <имя>    показать настройки профиля; секреты не печатаются
  rm <имя>      удалить профиль вместе с его секретами
  list          перечислить профили

Флаги у каждой подкоманды свои: ` + app.Name + ` profile add -h.

Пароль подключения, хеши звонков VK и ссылка csqtt:// не передаются
аргументами намеренно: аргумент попал бы в историю оболочки и в список
процессов. Спрашиваются они скрытым вводом, а в profiles.toml уходят только
ссылки на хранилище секретов.`,
		run: runProfile,
	}
}

// profileSubcommands — подкоманды в порядке помощи. Имя у каждой полное
// («profile set»): им подписываются ошибки и по нему же ищется подкоманда.
func profileSubcommands() []*command {
	return []*command{
		profileAddCommand(),
		profileSetCommand(),
		profileShowCommand(),
		profileRmCommand(),
		profileListCommand(),
	}
}

func profileSubNames() string {
	subs := profileSubcommands()
	names := make([]string, 0, len(subs))
	for _, sub := range subs {
		names = append(names, strings.TrimPrefix(sub.name, "profile "))
	}
	return strings.Join(names, ", ")
}

func runProfile(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return usagef("не названа подкоманда; бывают: %s", profileSubNames())
	}

	if isHelpFlag(args[0]) {
		fs := newFlagSet(profileCommand())
		fs.SetOutput(e.stdout)
		fs.Usage()
		return flag.ErrHelp
	}
	if strings.HasPrefix(args[0], "-") {
		// Своих флагов у profile нет, и «flag provided but not defined»
		// на месте подкоманды объясняло бы не то, что произошло.
		return usagef("флаги ставятся после подкоманды: %s profile <подкоманда> [флаги]", app.Name)
	}

	for _, sub := range profileSubcommands() {
		if sub.name == "profile "+args[0] {
			return sub.run(ctx, e, args[1:])
		}
	}
	return usagef("неизвестная подкоманда %q; бывают: %s", args[0], profileSubNames())
}

// profileFlags — поля профиля, которые задаются флагами add и set. Набор
// повторяет несекретные поля §10: что можно записать в файл, то можно задать
// и флагом.
type profileFlags struct {
	peer          *string
	workers       *int
	obfs          *string
	turnTransport *string
	datapath      *string
	socksListen   *string
	killSwitch    *bool
	vkHashMode    *string
	makeDefault   *bool
}

func addProfileFlags(fs *flag.FlagSet) *profileFlags {
	return &profileFlags{
		peer:          fs.String("peer", "", "сервер CSQTT: host или host:port (порт по умолчанию "+config.DefaultPeerPort+")"),
		workers:       fs.Int("workers", 0, "число воркеров: кратно 9, от 9 до 126, не больше 27 на хеш (в auto_js — не больше 72)"),
		obfs:          fs.String("obfs", "", "маскировка RTP: audio или video"),
		turnTransport: fs.String("turn-transport", "", "до TURN по udp или tcp_tls"),
		datapath:      fs.String("datapath", "", "как отдавать трафик: tun (весь трафик системы, нужен root; на Windows пока только с connect -allow-leak) или socks"),
		socksListen:   fs.String("socks-listen", "", "адрес SOCKS5 при datapath = socks, только loopback (по умолчанию "+datapath.DefaultSocksListen+")"),
		killSwitch:    fs.Bool("kill-switch", true, "kill-switch: "+killSwitchUsage+"; -kill-switch=false — выключить"),
		vkHashMode: fs.String("vk-hash-mode", "", "откуда звонок VK: manual — хеши профиля (по умолчанию) или auto_js — звонок создаёт ядро "+
			"по входу VK ("+app.Name+" vk login), хеши не нужны"),
		makeDefault: fs.Bool("default", false, "сделать профиль профилем по умолчанию"),
	}
}

// apply переносит в профиль названные флаги. Остальные поля остаются как были.
func (f *profileFlags) apply(p *config.Profile, named map[string]bool) {
	if named["peer"] {
		p.Peer = strings.TrimSpace(*f.peer)
	}
	if named["workers"] {
		p.Workers = *f.workers
	}
	if named["obfs"] {
		p.Obfs = strings.TrimSpace(*f.obfs)
	}
	if named["turn-transport"] {
		p.TurnTransport = strings.TrimSpace(*f.turnTransport)
	}
	if named["datapath"] {
		p.Datapath = strings.TrimSpace(*f.datapath)
	}
	if named["socks-listen"] {
		p.Socks.Listen = strings.TrimSpace(*f.socksListen)
	}
	if named["kill-switch"] {
		value := *f.killSwitch
		p.KillSwitch = &value
	}
	if named["vk-hash-mode"] {
		p.VkHashMode = strings.TrimSpace(*f.vkHashMode)
	}
}

// applyDefault — -default и -default=false.
func (f *profileFlags) applyDefault(file *config.File, name string, named map[string]bool) {
	if !named["default"] {
		return
	}
	switch {
	case *f.makeDefault:
		file.DefaultProfile = name
	case file.DefaultProfile == name:
		file.DefaultProfile = ""
	}
}

// --- profile add ---

func profileAddCommand() *command {
	return &command{
		name:    "profile add",
		args:    "<имя> [-link | -peer host[:port]] [флаги]",
		summary: "завести профиль из ссылки csqtt:// или по полям",
		description: `С -link ссылка csqtt:// из панели сервера CSQTT читается скрытым вводом (или
из stdin: ` + app.Name + ` profile add home -link < файл): в ней адрес сервера,
пароль и хеши звонков. Хешей в ссылке нет — они спрашиваются отдельно.

Без -link профиль заводится по полям: адрес сервера — флагом -peer, пароль
и хеши звонков VK спрашиваются скрытым вводом.

С -vk-hash-mode auto_js хеши не спрашиваются: звонок создаёт ядро по входу
VK (` + app.Name + ` vk login). Хеши из ссылки всё равно записываются — на случай
возврата к manual.

Остальные флаги задают поля профиля. Профиль с таким именем уже есть —
отказ: меняет профиль ` + app.Name + ` profile set.`,
		run: runProfileAdd,
	}
}

func runProfileAdd(ctx context.Context, e *env, args []string) error {
	cmd := profileAddCommand()
	if err := rejectSecretValues(cmd, args, "link"); err != nil {
		return err
	}
	fs := newFlagSet(cmd)
	configPath := addConfigFlag(fs)
	fields := addProfileFlags(fs)
	fromLink := fs.Bool("link", false, "взять адрес, пароль и хеши из ссылки csqtt:// (скрытым вводом)")
	rest, err := parseWithArgs(e, fs, args)
	if err != nil {
		return err
	}
	name, err := profileNameArg(cmd, rest)
	if err != nil {
		return err
	}
	if err := config.CheckProfileName(name); err != nil {
		return subUsagef(cmd, "%v", err)
	}
	named := setFlags(fs)
	if *fromLink && named["peer"] {
		return subUsagef(cmd, "адрес сервера — или из ссылки (-link), или флагом -peer, не оба сразу")
	}
	if !*fromLink && !named["peer"] {
		return subUsagef(cmd, "новый профиль заводится из ссылки (-link) или с адресом сервера (-peer host[:port])")
	}

	path, file, err := loadProfilesForWrite(*configPath)
	if err != nil {
		return err
	}
	if _, ok := file.Profiles[name]; ok {
		return fmt.Errorf("профиль %q уже есть: изменить его — %s profile set %s, заменить — сначала %s profile rm %s",
			name, app.Name, name, app.Name, name)
	}

	p := &config.Profile{Name: name}
	fields.apply(p, named)
	// Флаги проверяются до вопросов: ввести пароль дважды и услышать, что
	// -obfs набран с опечаткой, обидно. С -link адреса ещё нет — проверим,
	// когда он придёт из ссылки.
	if !*fromLink {
		if err := checkBeforeAsking(p); err != nil {
			return err
		}
	}

	var secrets profileSecrets
	if *fromLink {
		link, err := e.askLink(ctx)
		if err != nil {
			return err
		}
		p.Peer = link.Peer
		if err := checkBeforeAsking(p); err != nil {
			return err
		}
		secrets.password = link.Password
		secrets.hashes = link.Hashes
		if len(secrets.hashes) == 0 && !p.AutoJS() {
			fmt.Fprintln(e.stderr, "В ссылке нет хешей звонков VK.")
		}
	} else {
		secrets.password, err = e.askNewSecret(ctx,
			fmt.Sprintf("Пароль подключения для профиля %q (ввод не отображается): ", name),
			"Повторите пароль: ", "пароль подключения", true)
		if err != nil {
			return err
		}
	}
	if len(secrets.hashes) == 0 && !p.AutoJS() {
		if secrets.hashes, err = e.askHashes(ctx, name); err != nil {
			return err
		}
	}

	fields.applyDefault(file, name, named)
	return storeProfile(e, path, file, p, false, secrets)
}

// askLink спрашивает ссылку csqtt:// скрытым вводом. Ошибки разбора ссылку
// не повторяют (config.ParseShareLink).
func (e *env) askLink(ctx context.Context) (*config.ShareLink, error) {
	text, err := e.askSecret(ctx, "Ссылка csqtt:// из панели сервера (ввод не отображается): ")
	if errors.Is(err, errNoInput) {
		return nil, errors.New("ссылка не введена")
	}
	if err != nil {
		return nil, err
	}
	link, err := config.ParseShareLink(text)
	if err != nil {
		return nil, fmt.Errorf("ссылка: %w", err)
	}
	return link, nil
}

// askHashes спрашивает хеши звонков VK скрытым вводом и разбирает их так же,
// как ядро. Пусто или больше шести — отказ: ядро взяло бы первые шесть молча.
func (e *env) askHashes(ctx context.Context, name string) ([]string, error) {
	raw, err := e.askNewSecret(ctx,
		fmt.Sprintf("Хеши или ссылки на звонки VK для профиля %q, через запятую или пробел (ввод не отображается): ", name),
		"", "хеши звонков VK", false)
	if err != nil {
		return nil, err
	}
	hashes := config.ParseHashes(raw)
	switch {
	case len(hashes) == 0:
		return nil, errors.New("хеши звонков VK: во введённом ни одного хеша не разобрано")
	case len(hashes) > config.MaxHashes:
		return nil, fmt.Errorf("хеши звонков VK: разобрано %d, а ядро берёт не больше %d", len(hashes), config.MaxHashes)
	}
	return hashes, nil
}

// --- profile set ---

func profileSetCommand() *command {
	return &command{
		name:    "profile set",
		args:    "<имя> [флаги]",
		summary: "изменить профиль",
		description: `Флагом меняется одно поле профиля, остальные остаются как были.

Пароль и хеши звонков перезаписываются по -password и -hashes: значение
спрашивается скрытым вводом. Аргументом его передать нельзя, чтобы секрет не
попал в историю оболочки и в список процессов. Недоступный звонок профиль сам
не меняет — замените хеши здесь.

Файл настроек переписывается целиком: комментарии, если они там были,
не сохранятся.`,
		run: runProfileSet,
	}
}

func runProfileSet(ctx context.Context, e *env, args []string) error {
	cmd := profileSetCommand()
	if err := rejectSecretValues(cmd, args, "password", "hashes"); err != nil {
		return err
	}
	fs := newFlagSet(cmd)
	configPath := addConfigFlag(fs)
	fields := addProfileFlags(fs)
	askPassword := fs.Bool("password", false, "спросить пароль подключения заново (скрытым вводом)")
	askHashes := fs.Bool("hashes", false, "спросить хеши звонков VK заново (скрытым вводом)")
	rest, err := parseWithArgs(e, fs, args)
	if err != nil {
		return err
	}
	name, err := profileNameArg(cmd, rest)
	if err != nil {
		return err
	}
	named := setFlags(fs)

	path, file, err := loadProfiles(*configPath)
	if err != nil {
		return err
	}
	p, err := findProfile(file, name)
	if err != nil {
		return fmt.Errorf("%w; имя профиля — первым аргументом: %s profile set <имя> [флаги], "+
			"а пароль и хеши аргументами не передаются — их спрашивает скрытый ввод (-password, -hashes); "+
			"завести новый профиль — %s profile add <имя>", err, app.Name, app.Name)
	}
	if len(named) == 0 || len(named) == 1 && named["config"] {
		return subUsagef(cmd, "не названо, что менять: флаги — %s profile set -h", app.Name)
	}

	fields.apply(p, named)
	if err := checkBeforeAsking(p); err != nil {
		return err
	}

	var secrets profileSecrets
	if *askPassword {
		secrets.password, err = e.askNewSecret(ctx,
			fmt.Sprintf("Новый пароль подключения для профиля %q (ввод не отображается): ", name),
			"Повторите пароль: ", "пароль подключения", true)
		if err != nil {
			return err
		}
	}
	// Профиль, который возвращается из auto_js без хешей, без них не
	// запишется: хеши спрашиваются и без -hashes.
	if *askHashes || !p.AutoJS() && strings.TrimSpace(p.VkHashesRef) == "" {
		if secrets.hashes, err = e.askHashes(ctx, name); err != nil {
			return err
		}
	}

	fields.applyDefault(file, name, named)
	return storeProfile(e, path, file, p, true, secrets)
}

// profileSecrets — новые значения секретов профиля. Пустое — не менять.
type profileSecrets struct {
	password string
	hashes   []string
}

// storeProfile записывает профиль: секреты — в хранилище, профиль — в файл
// настроек, и говорит, что сделано. Значения секретов не печатаются — только
// где они лежат и сколько хешей.
func storeProfile(e *env, path string, file *config.File, p *config.Profile, existed bool, secrets profileSecrets) error {
	store := e.secretsFor(path)

	if secrets.password != "" {
		// Имя записи в хранилище — «<профиль>.password»: у каждого профиля
		// свои секреты, а Put по тому же имени перезаписывает прежнее значение
		// и не плодит осиротевших записей.
		ref, err := store.Put(p.Name+".password", secrets.password)
		if err != nil {
			return err
		}
		p.PasswordRef = ref
	}
	if len(secrets.hashes) > 0 {
		// Хранятся разобранные хеши через запятую — так же их получает ядро.
		ref, err := store.Put(p.Name+".vk_hashes", strings.Join(secrets.hashes, ","))
		if err != nil {
			return err
		}
		p.VkHashesRef = ref
	}

	if file.Profiles == nil {
		file.Profiles = map[string]*config.Profile{}
	}
	file.Profiles[p.Name] = p
	if err := config.Save(path, file); err != nil {
		return err
	}

	verb := "изменён"
	if !existed {
		verb = "создан"
	}
	fmt.Fprintf(e.stdout, "Профиль %q %s: %s\n", p.Name, verb, path)
	fmt.Fprintf(e.stdout, "  сервер: %s\n", config.PeerWithPort(p.Peer))
	if secrets.password != "" {
		fmt.Fprintf(e.stdout, "  пароль: записан (%s)\n", config.SecretRefKind(p.PasswordRef))
	}
	if len(secrets.hashes) > 0 {
		fmt.Fprintf(e.stdout, "  хешей:  %d (%s)\n", len(secrets.hashes), config.SecretRefKind(p.VkHashesRef))
	}
	if p.AutoJS() {
		fmt.Fprintln(e.stdout, "  звонок: создаёт ядро по входу VK (vk_hash_mode = auto_js)")
		if strings.TrimSpace(p.VkHashesRef) != "" {
			fmt.Fprintln(e.stdout, "Хеши профиля в режиме auto_js не используются: звонок создаёт ядро.")
		}
		if !file.VK.LoggedIn() {
			fmt.Fprintf(e.stdout, "Входа VK нет, а без него профиль не подключится: %s vk login\n", app.Name)
		}
		warnWorkers(e.stdout, p, 0)
	} else if len(secrets.hashes) > 0 {
		warnWorkers(e.stdout, p, len(secrets.hashes))
	}
	if file.DefaultProfile == p.Name {
		fmt.Fprintf(e.stdout, "Профиль по умолчанию: %s\n", p.Name)
	}
	return nil
}

// warnWorkers предупреждает, если воркеров в профиле больше, чем ядро
// запустит на этих хешах (27 на хеш, coreproc.NormalizeWorkers), а в auto_js
// — больше 72 (coreproc.NormalizeWorkersAutoJS; хеши тогда не считаются).
// Урежет число coreproc сам; предупреждение — чтобы число в профиле не
// вводило в заблуждение.
func warnWorkers(w io.Writer, p *config.Profile, hashes int) {
	want := p.Core().Workers
	if p.AutoJS() {
		if got := coreproc.NormalizeWorkersAutoJS(want); got < want {
			fmt.Fprintf(w, "Предупреждение: воркеров в профиле %d, а в режиме auto_js ядро запустит %d (не больше %d).\n",
				want, got, coreproc.MaxWorkersAutoJS)
		}
		return
	}
	if got := coreproc.NormalizeWorkers(want, hashes); got < want {
		fmt.Fprintf(w, "Предупреждение: воркеров в профиле %d, а на %d хеш(ах) ядро запустит %d (не больше 27 на хеш).\n",
			want, hashes, got)
	}
}

// checkBeforeAsking проверяет настройки до того, как спрашивать секреты.
//
// Ссылки на секреты подставляются заглушками — их ещё может не быть, а
// проверка требует непустых. Всё остальное проверяется по-настоящему и теми же
// сообщениями, что и при чтении файла.
func checkBeforeAsking(p *config.Profile) error {
	probe := *p
	if strings.TrimSpace(probe.PasswordRef) == "" {
		probe.PasswordRef = "проверка:подставится-ниже"
	}
	if strings.TrimSpace(probe.VkHashesRef) == "" {
		probe.VkHashesRef = "проверка:подставится-ниже"
	}
	return probe.Validate()
}

// --- profile show ---

func profileShowCommand() *command {
	return &command{
		name:    "profile show",
		args:    "<имя> [флаги]",
		summary: "показать настройки профиля; секреты не печатаются",
		description: `Значения, которых в профиле нет, показаны с пометкой «умолчание» — это то,
с чем профиль будет работать.

Пароль и хеши не печатаются: показывается только, где они лежат, читаются ли
на этой машине и сколько хешей.`,
		run: runProfileShow,
	}
}

func runProfileShow(_ context.Context, e *env, args []string) error {
	cmd := profileShowCommand()
	fs := newFlagSet(cmd)
	configPath := addConfigFlag(fs)
	rest, err := parseWithArgs(e, fs, args)
	if err != nil {
		return err
	}
	name, err := profileNameArg(cmd, rest)
	if err != nil {
		return err
	}

	path, file, err := loadProfiles(*configPath)
	if err != nil {
		return err
	}
	p, err := findProfile(file, name)
	if err != nil {
		return err
	}
	store := e.secretsFor(path)

	fmt.Fprintf(e.stdout, "профиль %s", p.Name)
	if isDefaultProfile(file, p.Name) {
		fmt.Fprint(e.stdout, " (по умолчанию)")
	}
	fmt.Fprintf(e.stdout, "\n%s\n\n", path)

	printField(e.stdout, "peer", config.PeerWithPort(p.Peer), "")
	printSetting(e.stdout, "datapath", p.Datapath, config.DefaultDatapath)
	printSetting(e.stdout, "workers", numberOrEmpty(p.Workers), strconv.Itoa(config.DefaultWorkers))
	printSetting(e.stdout, "obfs", p.Obfs, "audio")
	printSetting(e.stdout, "turn_transport", p.TurnTransport, "udp")
	printSetting(e.stdout, "vk_hash_mode", p.VkHashMode, config.VKHashModeManual)
	if p.KillSwitch == nil {
		printSetting(e.stdout, "kill_switch", "", "true")
	} else {
		printSetting(e.stdout, "kill_switch", strconv.FormatBool(*p.KillSwitch), "")
	}
	printSetting(e.stdout, "socks.listen", p.Socks.Listen, datapath.DefaultSocksListen)
	if strings.TrimSpace(p.DeviceID) == "" {
		printField(e.stdout, "device_id", "не задан", "(заведётся при подключении)")
	} else {
		printField(e.stdout, "device_id", p.DeviceID, "")
	}

	fmt.Fprintln(e.stdout)
	printField(e.stdout, "password", passwordNote(store, p.PasswordRef), "")
	hashesText, hashes := hashesNote(store, p.VkHashesRef)
	note := ""
	if p.AutoJS() {
		note = "(в режиме auto_js не нужны)"
	}
	printField(e.stdout, "vk_hashes", hashesText, note)
	if hashes > 0 || p.AutoJS() {
		warnWorkers(e.stdout, p, hashes)
	}
	return nil
}

// passwordNote — есть ли пароль и читается ли он на этой машине. Значения нет.
func passwordNote(store config.SecretStore, ref string) string {
	if strings.TrimSpace(ref) == "" {
		return "не задан"
	}
	if _, err := store.Get(ref); err != nil {
		return fmt.Sprintf("не читается: %v", err)
	}
	return fmt.Sprintf("задан (%s)", config.SecretRefKind(ref))
}

// hashesNote — сколько хешей и где они. Число хешей не секрет, а самая частая
// причина заглянуть в профиль.
func hashesNote(store config.SecretStore, ref string) (string, int) {
	if strings.TrimSpace(ref) == "" {
		return "не заданы", 0
	}
	value, err := store.Get(ref)
	if err != nil {
		return fmt.Sprintf("не читаются: %v", err), 0
	}
	n := len(config.ParseHashes(value))
	return fmt.Sprintf("%d (%s)", n, config.SecretRefKind(ref)), n
}

// --- profile rm ---

func profileRmCommand() *command {
	return &command{
		name:    "profile rm",
		args:    "<имя> [флаги]",
		summary: "удалить профиль вместе с его секретами",
		description: `Удаление необратимо: пароль и хеши стираются из хранилища, и вернуть их
можно только вводом заново. Поэтому команда спрашивает подтверждение;
-y отвечает за вас.`,
		run: runProfileRm,
	}
}

func runProfileRm(ctx context.Context, e *env, args []string) error {
	cmd := profileRmCommand()
	fs := newFlagSet(cmd)
	configPath := addConfigFlag(fs)
	yes := fs.Bool("y", false, "не спрашивать подтверждения")
	rest, err := parseWithArgs(e, fs, args)
	if err != nil {
		return err
	}
	name, err := profileNameArg(cmd, rest)
	if err != nil {
		return err
	}

	path, file, err := loadProfiles(*configPath)
	if err != nil {
		return err
	}
	p, err := findProfile(file, name)
	if err != nil {
		return err
	}

	if !*yes {
		agreed, err := e.confirm(ctx, fmt.Sprintf("Удалить профиль %q и его секреты?", name))
		if err != nil {
			return err
		}
		if !agreed {
			fmt.Fprintln(e.stdout, "Отменено, ничего не изменилось.")
			return nil
		}
	}

	store := e.secretsFor(path)
	for _, ref := range []string{p.PasswordRef, p.VkHashesRef} {
		if strings.TrimSpace(ref) == "" {
			continue
		}
		if err := store.Delete(ref); err != nil {
			// Секрет мог остаться от другой платформы или пропасть вручную.
			// Останавливаться нельзя: профиль остался бы удалённым наполовину.
			fmt.Fprintf(e.stderr, "Предупреждение: секрет %s не удалён: %v\n", ref, err)
		}
	}

	delete(file.Profiles, name)
	clearedDefault := false
	if file.DefaultProfile == name {
		file.DefaultProfile = ""
		clearedDefault = true
	}

	if err := config.Save(path, file); err != nil {
		return err
	}

	fmt.Fprintf(e.stdout, "Профиль %q удалён вместе с секретами: %s\n", name, path)
	if clearedDefault {
		fmt.Fprintln(e.stdout, "Он был профилем по умолчанию — теперь умолчания нет.")
	}
	return nil
}

// --- profile list ---

func profileListCommand() *command {
	return &command{
		name:    "profile list",
		args:    "[флаги]",
		summary: "перечислить профили",
		run:     runProfileList,
	}
}

func runProfileList(_ context.Context, e *env, args []string) error {
	cmd := profileListCommand()
	fs := newFlagSet(cmd)
	configPath := addConfigFlag(fs)
	rest, err := parseWithArgs(e, fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return subUsagef(cmd, "лишний аргумент: list аргументов не принимает")
	}

	path, file, err := loadProfiles(*configPath)
	if err != nil {
		return err
	}

	names := file.Names()
	if len(names) == 0 {
		fmt.Fprintf(e.stdout, "В %s нет ни одного профиля. Заведите: %s profile add <имя> -link\n", path, app.Name)
		return nil
	}

	width := 0
	for _, name := range names {
		width = max(width, len(name))
	}

	marked := false
	for _, name := range names {
		p := file.Profiles[name]
		marker := " "
		if isDefaultProfile(file, name) {
			marker = "*"
			marked = true
		}
		fmt.Fprintf(e.stdout, "%s %-*s  %-28s %s\n",
			marker, width, name, config.PeerWithPort(p.Peer), config.EffectiveDatapath(p.Datapath))
	}
	if marked {
		fmt.Fprintln(e.stdout, "\n* — профиль по умолчанию")
	}
	return nil
}

// isDefaultProfile повторяет правило File.Profile: сначала default_profile,
// а если его нет — единственный профиль в файле. Список обязан показывать то,
// что произойдёт при `csqtt-cli connect` без имени.
func isDefaultProfile(f *config.File, name string) bool {
	if def := strings.TrimSpace(f.DefaultProfile); def != "" {
		return def == name
	}
	return len(f.Profiles) == 1
}

// --- общее ---

// secretsFor — хранилище секретов рядом с файлом настроек.
func (e *env) secretsFor(configPath string) config.SecretStore {
	if e.secretStore != nil {
		return e.secretStore(configPath)
	}
	return config.NewSecretStoreFor(configPath)
}

// profileNameArg берёт имя профиля из позиционных аргументов. Оно обязательно
// у всех подкоманд, кроме list: `csqtt-cli profile rm` без имени не должен
// угадывать, что удалять.
//
// Лишний аргумент не повторяется в сообщении: чаще всего это значение,
// поставленное после -password или -hashes, то есть сам секрет.
func profileNameArg(cmd *command, positional []string) (string, error) {
	switch len(positional) {
	case 0:
		return "", subUsagef(cmd, "не названо имя профиля: %s %s <имя>", app.Name, cmd.name)
	case 1:
		return positional[0], nil
	default:
		return "", subUsagef(cmd, "лишний аргумент: профиль называется один, "+
			"а пароль и хеши аргументами не передаются — их спрашивает скрытый ввод (аргумент не печатается)")
	}
}

// findProfile ищет профиль строго по названному имени — для set, show и rm,
// где имя обязательно. File.Profile здесь не годится: он обрезает пробелы и
// пустое имя понимает как профиль по умолчанию, а `profile rm ""` не должен
// удалять профиль по умолчанию.
//
// Имени в сообщении нет, есть только список имеющихся: у set на место имени
// встаёт значение, поставленное после -password или -hashes, то есть сам
// секрет.
func findProfile(file *config.File, name string) (*config.Profile, error) {
	if p, ok := file.Profiles[name]; ok {
		return p, nil
	}
	names := file.Names()
	if len(names) == 0 {
		return nil, errors.New("такого профиля нет: в файле настроек нет ни одного профиля")
	}
	return nil, fmt.Errorf("такого профиля нет; в файле есть: %s", strings.Join(names, ", "))
}

// profilePath — какой файл настроек читать: названный флагом или тот, что
// полагается платформе.
func profilePath(flagValue string) (string, error) {
	if path := strings.TrimSpace(flagValue); path != "" {
		return path, nil
	}
	return config.Path()
}

// loadProfiles читает файл настроек. Отсутствие файла — ошибка с подсказкой:
// показать пустой список там, где файла нет вовсе, значит скрыть от человека,
// что он смотрит не туда.
func loadProfiles(flagValue string) (string, *config.File, error) {
	path, err := profilePath(flagValue)
	if err != nil {
		return "", nil, err
	}

	file, err := config.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil, fmt.Errorf("файла настроек нет: %s; заведите профиль: %s profile add <имя> -link", path, app.Name)
	}
	if err != nil {
		return "", nil, err
	}
	return path, file, nil
}

// loadProfilesForWrite — то же для add, которому отсутствие файла не помеха:
// первый профиль заводится в пустоте.
func loadProfilesForWrite(flagValue string) (string, *config.File, error) {
	path, err := profilePath(flagValue)
	if err != nil {
		return "", nil, err
	}

	file, err := config.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, &config.File{Profiles: map[string]*config.Profile{}}, nil
	}
	if err != nil {
		return "", nil, err
	}
	if file.Profiles == nil {
		file.Profiles = map[string]*config.Profile{}
	}
	return path, file, nil
}

func printField(w io.Writer, key, value, note string) {
	fmt.Fprintf(w, "  %-14s %s", key, value)
	if note != "" {
		fmt.Fprintf(w, "  %s", note)
	}
	fmt.Fprintln(w)
}

// printSetting печатает настройку и, если в профиле её нет, показывает
// умолчание с пометкой: важно то, с чем профиль будет работать.
func printSetting(w io.Writer, key, value, fallback string) {
	if strings.TrimSpace(value) == "" {
		printField(w, key, fallback, "(умолчание)")
		return
	}
	printField(w, key, value, "")
}

func numberOrEmpty(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}
