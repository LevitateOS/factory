//! Native development consumer of the production Lit component stories.
//! This workbench has no Factory backend, provider credentials or agent executor.
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;

use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};

struct RenderCheck {
    enabled: bool,
    diff: bool,
    reported: Arc<AtomicBool>,
}

#[tauri::command]
fn report(
    app: tauri::AppHandle,
    state: tauri::State<'_, RenderCheck>,
    cards: usize,
    light: String,
    dark: String,
) {
    if !state.enabled || state.diff || state.reported.swap(true, Ordering::SeqCst) {
        return;
    }
    let passed = cards >= 3 && light == "rgb(255, 255, 255)" && dark == "rgb(16, 16, 16)";
    println!("Native Factory feed: cards={cards}; light={light}; dark={dark}; passed={passed}");
    app.exit(if passed { 0 } else { 1 });
}

#[tauri::command]
fn diff_stage(state: tauri::State<'_, RenderCheck>, stage: String) {
    if state.enabled && state.diff && (stage == "selection" || stage == "draft") {
        println!("Native diff input ready: {stage}");
    }
}

#[tauri::command]
fn report_diff(
    app: tauri::AppHandle,
    state: tauri::State<'_, RenderCheck>,
    checks: Vec<String>,
    error: Option<String>,
) {
    if !state.enabled || !state.diff || state.reported.swap(true, Ordering::SeqCst) {
        return;
    }
    let required: Vec<&str> =
        serde_json::from_str(include_str!("../../../frontend/storybook/diff-checks.json"))
            .expect("valid shared diff check contract");
    let passed = error.is_none()
        && required
            .iter()
            .all(|name| checks.iter().any(|check| check == name));
    println!("Native Factory diff: checks={checks:?}; error={error:?}; passed={passed}");
    app.exit(if passed { 0 } else { 1 });
}

fn main() {
    if let Err(error) = run() {
        eprintln!("{error}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
    let mut check = false;
    let mut diff = false;
    for arg in std::env::args().skip(1) {
        if arg == "--check" && !check {
            check = true;
        } else if arg == "--check-diff" && !check {
            check = true;
            diff = true;
        } else {
            return Err("usage: soda-factory-ui [--check | --check-diff]".into());
        }
    }
    let reported = Arc::new(AtomicBool::new(false));
    let timeout_reported = Arc::clone(&reported);
    tauri::Builder::default()
        .manage(RenderCheck {
            enabled: check,
            diff,
            reported,
        })
        .invoke_handler(tauri::generate_handler![report, diff_stage, report_diff])
        .setup(move |app| {
            let page = if diff {
                "iframe.html?id=factory-review--renamed&viewMode=story&nativeDiffCheck=true"
            } else if check {
                "iframe.html?id=factory-feed--activity&viewMode=story"
            } else {
                "index.html?path=/story/factory-feed--activity"
            };
            WebviewWindowBuilder::new(app, "main", WebviewUrl::App(page.into()))
                .title("Factory component workbench")
                .inner_size(1200.0, 1000.0)
                .build()?;
            if check {
                let handle = app.handle().clone();
                std::thread::spawn(move || {
                    std::thread::sleep(Duration::from_secs(if diff { 90 } else { 30 }));
                    if !timeout_reported.load(Ordering::SeqCst) {
                        eprintln!("Native component rendering timed out");
                        handle.exit(1);
                    }
                });
            }
            Ok(())
        })
        .on_page_load(|window, _| {
            if !window.state::<RenderCheck>().enabled || window.state::<RenderCheck>().diff {
                return;
            }
            let _ = window.eval(
                r#"
                const timer = setInterval(() => {
                    const root = document.querySelector('.soda-factory-root');
                    const cards = document.querySelectorAll('article.soda-feed-post');
                    if (!root || cards.length < 3 || document.fonts.status !== 'loaded') return;
                    clearInterval(timer);
                    root.dataset.factoryScheme = 'light';
                    const light = getComputedStyle(root).backgroundColor;
                    root.dataset.factoryScheme = 'dark';
                    const dark = getComputedStyle(root).backgroundColor;
                    window.__TAURI__.core.invoke('report', {cards: cards.length, light, dark});
                }, 100);
            "#,
            );
        })
        .run(tauri::generate_context!())?;
    Ok(())
}
