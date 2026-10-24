// Правка П1 csqtt-cli (doc/architecture.md, Р6): пароль и хеши VK — первой
// строкой stdin, а не аргументами. Командную строку процесса видит любой
// пользователь машины, а из пароля выводится WRAP-ключ.
//
// Файл не из апстрима: его кладёт в core/ scripts/vendorcore из
// scripts/vendorcore/files, там его и править.
//
// С флагом -secrets-stdin ядро до всего остального ждёт строку
// `SECRETS:<base64 json>`, где json — {"password": "…", "vk": "…"}, а vk — то
// же, что значение -vk (в режиме auto_js его может не быть). Строка читается
// из std::io::stdin(): то, что пришло следом (VK_JS_BOOTSTRAP:, команды),
// остаётся в его общем буфере, и читатель stdin ядра, который берёт тот же
// std::io::stdin(), получит это следом.
//
// Пароль у апстрима (с 2.5.0) — переменная окружения CSQTT_PASSWORD, флага
// --password нет; поле Arguments::password добавлено правилом как #[arg(skip)]:
// аргументом его не передать. С -secrets-stdin пароль берётся отсюда, а
// окружение не читается (password); без флага — как у апстрима.
//
// Ни одно сообщение об ошибке не содержит значений: журнал ядра оболочка
// пишет в файл.

use anyhow::{Context, Result, anyhow, bail};
use base64::{Engine, engine::general_purpose::STANDARD};
use serde::Deserialize;
use std::{
    io::{BufRead, Read},
    sync::mpsc,
    time::Duration,
};

const PREFIX: &str = "SECRETS:";
/// Предел строки вместе с переводом строки: шесть хешей и пароль в base64 —
/// меньше килобайта.
const MAX_LINE: u64 = 16 * 1024;
const TIMEOUT: Duration = Duration::from_secs(15);

/// Без Debug: значения не должны попасть в журнал и случайно.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Secrets {
    password: String,
    #[serde(default)]
    vk: String,
}

/// Без -secrets-stdin возвращает аргументы как есть. С ним — дочитывает пароль
/// и хеши из stdin, а при ошибке завершает процесс, как main при ошибке
/// создания рантайма.
pub(crate) fn apply_or_exit(arguments: crate::Arguments) -> crate::Arguments {
    if !arguments.secrets_stdin {
        return arguments;
    }
    match apply(arguments, || std::io::stdin().lock(), TIMEOUT) {
        Ok(arguments) => arguments,
        Err(error) => {
            eprintln!("[ФАТАЛ] {error:#}");
            std::process::exit(1);
        }
    }
}

/// Пароль подключения: с -secrets-stdin — из строки SECRETS (apply), без
/// флага — из окружения, как у апстрима.
pub(crate) fn password(arguments: &crate::Arguments) -> String {
    if arguments.secrets_stdin {
        arguments.password.clone()
    } else {
        std::env::var("CSQTT_PASSWORD").unwrap_or_default()
    }
}

fn apply<R: BufRead>(
    mut arguments: crate::Arguments,
    open: impl FnOnce() -> R + Send + 'static,
    timeout: Duration,
) -> Result<crate::Arguments> {
    // Хеши в аргументах при флаге — значит, оболочка всё же передала их
    // командной строкой, и прятать уже нечего. Пароль аргументом не
    // принимается вовсе (#[arg(skip)]).
    if !arguments.vk.is_empty() {
        bail!("[КЛИЕНТ] С -secrets-stdin пароль и хеши VK аргументами не передаются");
    }
    let secrets = parse(&read_line_with_timeout(open, timeout)?)?;
    arguments.password = secrets.password;
    arguments.vk = secrets.vk;
    Ok(arguments)
}

/// Чтение — в своём потоке: у блокирующего stdin нет тайм-аута. Если строка не
/// пришла, поток остаётся ждать, а процесс завершается с ошибкой.
fn read_line_with_timeout<R: BufRead>(
    open: impl FnOnce() -> R + Send + 'static,
    timeout: Duration,
) -> Result<String> {
    let (sender, receiver) = mpsc::channel();
    std::thread::spawn(move || {
        let _ = sender.send(read_line(&mut open()));
    });
    match receiver.recv_timeout(timeout) {
        Ok(line) => line,
        Err(mpsc::RecvTimeoutError::Timeout) => bail!(
            "[КЛИЕНТ] Тайм-аут передачи секретов: нет строки SECRETS за {} с",
            timeout.as_secs_f32()
        ),
        Err(mpsc::RecvTimeoutError::Disconnected) => {
            bail!("[КЛИЕНТ] Чтение секретов прервано")
        }
    }
}

/// Одна строка, не длиннее MAX_LINE. Лишнего из reader не забирает: Take
/// отдаёт BufRead не больше предела, а read_line берёт ровно до \n.
fn read_line(reader: &mut impl BufRead) -> Result<String> {
    let mut line = String::new();
    let read = reader
        .take(MAX_LINE)
        .read_line(&mut line)
        .map_err(|_| anyhow!("[КЛИЕНТ] Секреты не прочитаны: ошибка stdin или не UTF-8"))?;
    if read == 0 {
        bail!("[КЛИЕНТ] stdin закрыт до строки SECRETS");
    }
    if !line.ends_with('\n') && read as u64 == MAX_LINE {
        bail!("[КЛИЕНТ] Строка секретов длиннее {MAX_LINE} байт");
    }
    Ok(line)
}

fn parse(line: &str) -> Result<Secrets> {
    let encoded = line
        .trim()
        .strip_prefix(PREFIX)
        .context("[КЛИЕНТ] Первая строка stdin — не SECRETS:")?;
    let decoded = STANDARD
        .decode(encoded)
        .map_err(|_| anyhow!("[КЛИЕНТ] Повреждены секреты: не base64"))?;
    serde_json::from_slice(&decoded)
        .map_err(|_| anyhow!("[КЛИЕНТ] Повреждены секреты: нужен JSON с password и vk"))
}

#[cfg(test)]
mod tests {
    use super::*;
    use clap::Parser;
    use std::io::Cursor;

    fn line(json: &str) -> String {
        format!("{PREFIX}{}\n", STANDARD.encode(json))
    }

    fn arguments(extra: &[&str]) -> crate::Arguments {
        let mut all = vec!["csqtt-client", "--peer", "127.0.0.1:9000", "--secrets-stdin"];
        all.extend_from_slice(extra);
        crate::Arguments::try_parse_from(all).unwrap()
    }

    fn apply_input(arguments: crate::Arguments, input: String) -> Result<crate::Arguments> {
        apply(arguments, move || Cursor::new(input), Duration::from_secs(5))
    }

    fn error(result: Result<impl Sized>) -> String {
        match result {
            Ok(_) => panic!("ожидалась ошибка"),
            Err(error) => format!("{error:#}"),
        }
    }

    #[test]
    fn flag_parses_with_single_dash() {
        assert_eq!(
            crate::normalize_cli_argument("-secrets-stdin".to_owned()),
            "--secrets-stdin"
        );
        assert!(arguments(&[]).secrets_stdin);
        let without = crate::Arguments::try_parse_from(["csqtt-client"]).unwrap();
        assert!(!without.secrets_stdin);
    }

    #[test]
    fn secrets_fill_password_and_hashes() {
        let got = apply_input(
            arguments(&[]),
            line(r#"{"password":"пароль","vk":"-Wabc,def"}"#),
        )
        .unwrap();
        assert_eq!(got.password, "пароль");
        assert_eq!(got.vk, "-Wabc,def");
        assert_eq!(got.peer, "127.0.0.1:9000");
    }

    #[test]
    fn vk_is_optional_for_auto_js() {
        let got = apply_input(arguments(&[]), line(r#"{"password":"p"}"#)).unwrap();
        assert_eq!(got.password, "p");
        assert_eq!(got.vk, "");
    }

    #[test]
    fn line_without_newline_before_eof_is_accepted() {
        let input = line(r#"{"password":"p","vk":"h"}"#).trim_end().to_owned();
        assert_eq!(apply_input(arguments(&[]), input).unwrap().vk, "h");
    }

    #[test]
    fn next_line_stays_in_reader() {
        let mut input = Cursor::new(format!(
            "{}VK_JS_BOOTSTRAP:e30=\nSTOP\n",
            line(r#"{"password":"p"}"#)
        ));
        read_line(&mut input).unwrap();
        let mut rest = String::new();
        input.read_to_string(&mut rest).unwrap();
        assert_eq!(rest, "VK_JS_BOOTSTRAP:e30=\nSTOP\n");
    }

    #[test]
    fn secrets_in_arguments_are_rejected() {
        let message = error(apply_input(
            arguments(&["--vk", "-Wabc"]),
            line(r#"{"password":"p","vk":"h"}"#),
        ));
        assert!(message.contains("аргументами не передаются"), "{message}");
        // Пароль — не аргумент: clap его не знает.
        assert!(
            crate::Arguments::try_parse_from(["csqtt-client", "--password", "p"]).is_err()
        );
        assert!(
            crate::Arguments::try_parse_from(["csqtt-client", "--password=p"]).is_err()
        );
    }

    /// С флагом пароль — из SECRETS, окружение не читается; без флага — как
    /// у апстрима, из CSQTT_PASSWORD.
    #[test]
    fn password_comes_from_secrets_only_with_the_flag() {
        let got = apply_input(arguments(&[]), line(r#"{"password":"p"}"#)).unwrap();
        assert_eq!(password(&got), "p");
        let without = crate::Arguments::try_parse_from(["csqtt-client"]).unwrap();
        assert!(without.password.is_empty());
        assert_eq!(
            password(&without),
            std::env::var("CSQTT_PASSWORD").unwrap_or_default()
        );
    }

    #[test]
    fn too_long_line_is_rejected_without_reading_past_limit() {
        let mut input = Cursor::new(format!("{PREFIX}{}\n", "A".repeat(MAX_LINE as usize)));
        let message = error(read_line(&mut input));
        assert!(message.contains("длиннее"), "{message}");
        assert_eq!(input.position(), MAX_LINE);
    }

    #[test]
    fn line_of_exactly_max_length_is_accepted() {
        let mut input = Cursor::new(format!("{}\n", "A".repeat(MAX_LINE as usize - 1)));
        assert_eq!(read_line(&mut input).unwrap().len(), MAX_LINE as usize);
    }

    #[test]
    fn garbage_is_rejected_without_echoing_values() {
        let cases = [
            (String::new(), "закрыт до строки SECRETS"),
            ("\n".to_owned(), "не SECRETS:"),
            ("STOP\n".to_owned(), "не SECRETS:"),
            ("VK_JS_BOOTSTRAP:e30=\n".to_owned(), "не SECRETS:"),
            ("SECRETS:не-base64-тайна\n".to_owned(), "не base64"),
            (line("тайна"), "нужен JSON"),
            (line(r#"{"vk":"тайна"}"#), "нужен JSON"),
            (line(r#"{"password":"тайна","vk":["h"]}"#), "нужен JSON"),
            (line(r#"{"password":"тайна","token":"x"}"#), "нужен JSON"),
            (line(r#"{"password":7}"#), "нужен JSON"),
        ];
        for (input, want) in cases {
            let message = error(apply_input(arguments(&[]), input.clone()));
            assert!(message.contains(want), "{input:?}: {message}");
            assert!(!message.contains("тайна") && !message.contains('7'), "{message}");
        }
    }

    #[test]
    fn invalid_utf8_is_rejected() {
        let mut input = Cursor::new(b"SECRETS:\xff\xfe\n".to_vec());
        assert!(error(read_line(&mut input)).contains("не UTF-8"));
    }

    /// Строка так и не пришла: stdin открыт, но молчит.
    #[test]
    fn missing_line_times_out() {
        let (_keep_open, receiver) = mpsc::channel::<()>();
        let silent = move || {
            let _ = receiver.recv();
            Cursor::new(Vec::new())
        };
        let started = std::time::Instant::now();
        let message = error(apply(arguments(&[]), silent, Duration::from_millis(200)));
        assert!(message.contains("Тайм-аут"), "{message}");
        assert!(started.elapsed() < Duration::from_secs(5));
    }
}
