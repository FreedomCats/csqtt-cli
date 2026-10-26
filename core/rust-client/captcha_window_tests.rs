// Правка П5 csqtt-cli (doc/architecture.md, Р7): тесты окна капчи человеку —
// ручного (manual) и режима wv (selected). Модуль — дочерний к captcha.rs (его
// подключает правило vendorcore), чтобы видеть закрытый CaptchaSolver::webview.
//
// Файл не из апстрима: его кладёт в core/ scripts/vendorcore из
// scripts/vendorcore/files, там его и править.
//
// Часы — виртуальные (start_paused): когда все задачи ждут таймеров, tokio
// сразу переводит время к ближайшему, и три минуты проходят мгновенно.

use super::*;
use tokio::time::Instant;

fn captcha() -> VkCaptchaError {
    VkCaptchaError {
        error_code: 14,
        error_msg: "Captcha needed".to_owned(),
        captcha_sid: "1".to_owned(),
        captcha_img: String::new(),
        redirect_uri: "https://id.vk.com/not_robot_captcha?session_token=fake".to_owned(),
        session_token: "fake".to_owned(),
        captcha_timestamp: "0".to_owned(),
        captcha_attempt: "1".to_owned(),
    }
}

fn open_manual_window(solver: &Arc<CaptchaSolver>) -> tokio::task::JoinHandle<Result<String>> {
    let solver = solver.clone();
    tokio::spawn(async move {
        solver
            .webview(0, &captcha(), "manual", MANUAL_WEBVIEW_TIMEOUT)
            .await
    })
}

// Режим wv — через solve, как его зовёт воркер: решатель ядра (Rust v2) в нём
// не вызывается, и клиенту не нужно ходить в сеть.
fn open_wv_window(solver: &Arc<CaptchaSolver>) -> tokio::task::JoinHandle<Result<String>> {
    let solver = solver.clone();
    tokio::spawn(async move {
        let client = Client::builder().build().unwrap();
        solver
            .solve(&client, &Profile::default(), None, 0, &captcha())
            .await
    })
}

#[test]
fn manual_window_is_three_minutes() {
    assert_eq!(MANUAL_WEBVIEW_TIMEOUT, Duration::from_secs(180));
}

#[tokio::test(start_paused = true)]
async fn manual_window_accepts_a_token_after_the_upstream_minute() {
    let solver = CaptchaSolver::new("auto", CancellationToken::new());
    let window = open_manual_window(&solver);
    tokio::time::sleep(Duration::from_secs(179)).await;
    assert!(solver.submit_result("token-after-three-minutes".to_owned()));
    assert_eq!(window.await.unwrap().unwrap(), "token-after-three-minutes");
}

#[tokio::test(start_paused = true)]
async fn manual_window_closes_after_three_minutes() {
    let solver = CaptchaSolver::new("auto", CancellationToken::new());
    let opened = Instant::now();
    let error = open_manual_window(&solver).await.unwrap().unwrap_err();
    assert_eq!(opened.elapsed(), MANUAL_WEBVIEW_TIMEOUT);
    assert!(format!("{error:#}").contains("timed out"), "{error:#}");
}

#[tokio::test(start_paused = true)]
async fn wv_window_accepts_a_token_after_the_upstream_two_minutes() {
    let solver = CaptchaSolver::new("wv", CancellationToken::new());
    let window = open_wv_window(&solver);
    tokio::time::sleep(Duration::from_secs(179)).await;
    assert!(solver.submit_result("token-after-three-minutes".to_owned()));
    assert_eq!(window.await.unwrap().unwrap(), "token-after-three-minutes");
}

#[tokio::test(start_paused = true)]
async fn wv_window_closes_after_three_minutes() {
    let solver = CaptchaSolver::new("wv", CancellationToken::new());
    let opened = Instant::now();
    let error = open_wv_window(&solver).await.unwrap().unwrap_err();
    assert_eq!(opened.elapsed(), MANUAL_WEBVIEW_TIMEOUT);
    assert!(format!("{error:#}").contains("timed out"), "{error:#}");
}
