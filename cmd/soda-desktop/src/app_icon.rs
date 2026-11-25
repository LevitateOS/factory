//! The Dock icon chosen in Settings. The choice is kept in the profile's
//! `app-icon` file; dark is the default. macOS shows the chosen tile in the
//! Dock; other systems keep their installed icon and report it unavailable.
use serde::{Deserialize, Serialize};
use std::path::Path;
use tauri::{AppHandle, WebviewWindow};

#[derive(Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum AppIcon {
    #[default]
    Dark,
    Light,
}

const FILE: &str = "app-icon";

impl AppIcon {
    pub fn read(profile: &Path) -> Self {
        match std::fs::read_to_string(profile.join(FILE)).as_deref().map(str::trim) {
            Ok("light") => Self::Light,
            _ => Self::Dark,
        }
    }
    fn save(self, profile: &Path) -> std::io::Result<()> {
        let name = match self {
            Self::Dark => "dark",
            Self::Light => "light",
        };
        let staged = profile.join(".app-icon.tmp");
        std::fs::write(&staged, name)?;
        std::fs::rename(staged, profile.join(FILE))
    }
}

/// Shows `icon` in the Dock. Runs on the main thread, as AppKit requires.
pub fn apply(app: &AppHandle, icon: AppIcon) {
    #[cfg(target_os = "macos")]
    let _ = app.run_on_main_thread(move || {
        use objc2::{AllocAnyThread, MainThreadMarker};
        use objc2_app_kit::{NSApplication, NSImage};
        use objc2_foundation::NSData;
        let png: &[u8] = match icon {
            AppIcon::Dark => include_bytes!("../../../assets/branding/icons/app/factory-dark.png"),
            AppIcon::Light => include_bytes!("../../../assets/branding/icons/app/factory-light.png"),
        };
        let Some(mtm) = MainThreadMarker::new() else { return };
        let Some(image) = NSImage::initWithData(NSImage::alloc(), &NSData::with_bytes(png)) else { return };
        unsafe { NSApplication::sharedApplication(mtm).setApplicationIconImage(Some(&image)) };
    });
    #[cfg(not(target_os = "macos"))]
    let _ = (app, icon);
}

#[derive(Serialize)]
pub struct View {
    icon: AppIcon,
    available: bool,
}

fn view(icon: AppIcon) -> View {
    View { icon, available: cfg!(target_os = "macos") }
}

#[tauri::command]
pub fn app_icon_status(window: WebviewWindow, state: tauri::State<'_, crate::Desktop>) -> Result<View, String> {
    crate::caller(&window)?;
    Ok(view(*state.app_icon.lock().map_err(|_| "app icon unavailable")?))
}

#[tauri::command]
pub fn app_icon_select(
    window: WebviewWindow,
    app: AppHandle,
    state: tauri::State<'_, crate::Desktop>,
    icon: AppIcon,
) -> Result<View, String> {
    crate::caller(&window)?;
    let mut current = state.app_icon.lock().map_err(|_| "app icon unavailable")?;
    icon.save(&state.paths.profile).map_err(|_| "app icon could not be saved")?;
    *current = icon;
    apply(&app, icon);
    Ok(view(icon))
}
