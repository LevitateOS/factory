//! Isolated real Fountain/package/Factory admission check. No retained host,
//! account, container, model provider, PAT or external repository is selected.
use serde_json::{json, Value};
use std::error::Error;
use std::fs::{self, OpenOptions};
use std::io::Write;
use std::net::TcpListener;
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};

#[path = "../fountain_issue_browser.rs"]
mod fountain_issue_browser;
#[path = "../fountain_read_edges.rs"]
mod fountain_read_edges;
#[path = "../fountain_read_issues.rs"]
mod fountain_read_issues;
#[path = "../fountain_read_pull_checks.rs"]
mod fountain_read_pull_checks;
#[path = "../fountain_read_pull_code.rs"]
mod fountain_read_pull_code;

type Result<T> = std::result::Result<T, Box<dyn Error>>;

fn private_file(path: &Path, bytes: &[u8]) -> Result<()> {
    OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(path)?
        .write_all(bytes)?;
    Ok(())
}
fn port() -> Result<u16> {
    Ok(TcpListener::bind("127.0.0.1:0")?.local_addr()?.port())
}
fn require(ok: bool, message: &str) -> Result<()> {
    if ok {
        Ok(())
    } else {
        Err(message.into())
    }
}
fn checked(command: &mut Command, label: &str) -> Result<Vec<u8>> {
    let out = command.output()?;
    require(out.status.success(), label)?;
    Ok(out.stdout)
}
fn checked_private(command: &mut Command, label: &str, log: &Path) -> Result<Vec<u8>> {
    let out = command.output()?;
    let mut bytes = out.stdout.clone();
    bytes.extend_from_slice(&out.stderr);
    private_file(log, &bytes)?;
    require(out.status.success(), label)?;
    Ok(out.stdout)
}

struct OwnedChild {
    child: Child,
}
impl OwnedChild {
    fn spawn(command: &mut Command, log: &Path) -> Result<Self> {
        let file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(log)?;
        Ok(Self {
            child: command
                .process_group(0)
                .stdin(Stdio::null())
                .stdout(file.try_clone()?)
                .stderr(file)
                .spawn()?,
        })
    }
    fn stop(&mut self) -> Result<()> {
        if self.child.try_wait()?.is_some() {
            return Err("owned server exited before shutdown".into());
        }
        unsafe {
            libc::kill(self.child.id() as i32, libc::SIGTERM);
        }
        let until = Instant::now() + Duration::from_secs(25);
        loop {
            if let Some(status) = self.child.try_wait()? {
                return require(status.success(), "owned server shutdown failed");
            }
            require(
                Instant::now() < until,
                "owned server shutdown exceeded deadline",
            )?;
            thread::sleep(Duration::from_millis(100));
        }
    }
}
impl Drop for OwnedChild {
    fn drop(&mut self) {
        // Only process groups created by this invocation. No caller PID input.
        unsafe {
            libc::kill(-(self.child.id() as i32), libc::SIGKILL);
        }
        let _ = self.child.wait();
    }
}

struct Browser {
    root: PathBuf,
    origin: String,
    count: usize,
}
impl Browser {
    fn request(
        &mut self,
        path: &str,
        post: Option<&str>,
        generation: Option<&str>,
    ) -> Result<(u16, String, String)> {
        self.count += 1;
        let headers = self.root.join(format!("http-{}.headers", self.count));
        let body = self.root.join(format!("http-{}.body", self.count));
        private_file(&headers, b"")?;
        private_file(&body, b"")?;
        let mut command = Command::new("curl");
        command
            .args(["--silent", "--show-error", "--max-time", "10", "--cacert"])
            .arg(self.root.join("cert.pem"))
            .arg("--cookie")
            .arg(self.root.join("cookies"))
            .arg("--cookie-jar")
            .arg(self.root.join("cookies"))
            .arg("--dump-header")
            .arg(&headers)
            .arg("--output")
            .arg(&body)
            .args(["--write-out", "%{http_code}"]);
        if let Some(form) = post {
            command
                .arg("--header")
                .arg(format!("Origin: {}", self.origin))
                .args(["--header", "Sec-Fetch-Site: same-origin"]);
            let file = self.root.join(format!("http-{}.form", self.count));
            private_file(&file, form.as_bytes())?;
            command
                .args([
                    "--header",
                    "Content-Type: application/x-www-form-urlencoded",
                    "--data-binary",
                ])
                .arg(format!("@{}", file.display()));
        }
        if let Some(value) = generation {
            command
                .arg("--header")
                .arg(format!("X-Extension-Session-Generation: {value}"));
        }
        command.arg(format!("{}{}", self.origin, path));
        let status = checked(&mut command, "native browser HTTP failed")?;
        Ok((
            std::str::from_utf8(&status)?.parse()?,
            fs::read_to_string(body)?,
            fs::read_to_string(headers)?,
        ))
    }
}
fn form(fields: &[(&str, &str)]) -> String {
    url::form_urlencoded::Serializer::new(String::new())
        .extend_pairs(fields.iter().copied())
        .finish()
}

fn run() -> Result<()> {
    let mut args: Vec<_> = std::env::args_os().skip(1).collect();
    let issue_browser = args.first().is_some_and(|s| s == "--issues-browser");
    let issues = issue_browser || args.first().is_some_and(|s| s == "--issues");
    let pull_code = args.first().is_some_and(|s| s == "--pull-code");
    let pull_checks = args.first().is_some_and(|s| s == "--pull-checks");
    let read_edges = args.first().is_some_and(|s| s == "--read-edges");
    if issues || pull_code || pull_checks || read_edges {
        args.remove(0);
    }
    require(args.len()==3,"usage: factory-fountain-host-check [--issues | --issues-browser | --pull-code | --pull-checks | --read-edges] STAGE FOUNTAIN_SOURCE NEW_PROFILE (inside soda-native-pg-check --once)")?;
    let stage = fs::canonicalize(PathBuf::from(&args[0]))?;
    let source = fs::canonicalize(PathBuf::from(&args[1]))?;
    let root = PathBuf::from(&args[2]);
    require(
        root.is_absolute()
            && root.starts_with("/home")
            && root.parent().is_some_and(Path::is_dir)
            && fs::symlink_metadata(&root).is_err(),
        "fresh owned /home profile required",
    )?;
    fs::create_dir(&root)?;
    fs::set_permissions(&root, fs::Permissions::from_mode(0o700))?;
    let root = fs::canonicalize(root)?;
    require(
        root.to_str().is_some_and(|s| {
            s.bytes()
                .all(|c| c.is_ascii_alphanumeric() || b"/._-".contains(&c))
        }),
        "profile must use plain path characters",
    )?;
    let dsn = PathBuf::from(
        std::env::var_os("SODA_NATIVE_PG_DSN_FILE")
            .ok_or("managed PostgreSQL DSN file required")?,
    );
    let uid = unsafe { libc::geteuid() };
    require(uid != 0, "native host check runs as an ordinary user")?;
    let user = String::from_utf8(checked(
        Command::new("id").arg("-un"),
        "native user lookup failed",
    )?)?
    .trim()
    .to_string();
    let native_port = port()?;
    let factory_port = port()?;
    let origin = format!("https://127.0.0.1:{native_port}");
    checked(
        Command::new("openssl")
            .args([
                "req",
                "-x509",
                "-newkey",
                "rsa:2048",
                "-nodes",
                "-days",
                "1",
                "-subj",
                "/CN=localhost",
                "-addext",
                "subjectAltName=IP:127.0.0.1,DNS:localhost",
                "-keyout",
            ])
            .arg(root.join("key.pem"))
            .arg("-out")
            .arg(root.join("cert.pem")),
        "native TLS certificate failed",
    )?;
    fs::set_permissions(root.join("key.pem"), fs::Permissions::from_mode(0o600))?;
    let ini = root.join("app.ini");
    let work = root.join("fountain");
    fs::create_dir(&work)?;
    private_file(&ini,format!("RUN_USER = {user}\nRUN_MODE = prod\n[server]\nAPP_DATA_PATH = {r}/data\nPROTOCOL = https\nHTTP_ADDR = 127.0.0.1\nHTTP_PORT = {native_port}\nROOT_URL = {origin}/\nSTATIC_ROOT_PATH = {s}\nDISABLE_SSH = true\nOFFLINE_MODE = true\nCERT_FILE = {r}/cert.pem\nKEY_FILE = {r}/key.pem\n[database]\nDB_TYPE = sqlite3\nPATH = {r}/fountain.db\n[repository]\nROOT = {r}/repositories\n[security]\nINSTALL_LOCK = true\n[service]\nDISABLE_REGISTRATION = true\n[session]\nPROVIDER = file\nPROVIDER_CONFIG = {r}/sessions\nCOOKIE_SECURE = true\n[extensions]\nENABLED = true\nPATH = {r}/extensions\nSERVICE_CALLBACK_PATH = {r}/callback.sock\n[log]\nMODE = console\nLEVEL = Warn\n[actions]\nENABLED = true\n",r=root.display(),s=source.display()).as_bytes())?;
    let fountain = stage.join("fountain");
    let cli = |tail: &[&str]| {
        let mut c = Command::new(&fountain);
        c.arg("--work-path")
            .arg(&work)
            .arg("--config")
            .arg(&ini)
            .args(tail);
        c
    };
    checked_private(
        &mut cli(&["migrate"]),
        "native migration failed (profile retained)",
        &root.join("cli-migration.log"),
    )?;
    let created = checked_private(
        &mut cli(&[
            "admin",
            "user",
            "create",
            "--username",
            "factory-check",
            "--email",
            "factory-check@example.invalid",
            "--admin",
            "--random-password",
            "--must-change-password=false",
        ]),
        "native account creation failed",
        &root.join("cli-account.log"),
    )?;
    let text = std::str::from_utf8(&created)?;
    let password = text
        .lines()
        .find_map(|line| {
            line.strip_prefix("generated random password is '")
                .and_then(|s| s.strip_suffix('\''))
        })
        .ok_or("native generated password unavailable")?;
    private_file(&root.join("password"), password.as_bytes())?;
    let package = root.join("package");
    checked(
        Command::new("cp")
            .arg("-a")
            .arg(stage.join("package"))
            .arg(&package),
        "stage package copy failed",
    )?;
    fs::copy(stage.join("bin/soda-extension"), package.join("backend"))?;
    private_file(&package.join("run"),format!("#!/bin/sh\nexec ./backend --package-id=soda --soda-socket={r}/absent-forge.sock --factory-socket={r}/factory.sock\n",r=root.display()).as_bytes())?;
    fs::set_permissions(package.join("run"), fs::Permissions::from_mode(0o700))?;
    checked_private(
        &mut cli(&[
            "extensions",
            "install",
            package.to_str().ok_or("package path")?,
        ]),
        "native package installation failed",
        &root.join("cli-install.log"),
    )?;
    checked_private(
        &mut cli(&["extensions", "enable", "soda"]),
        "native package activation failed",
        &root.join("cli-activate.log"),
    )?;
    private_file(
        &root.join("factory.json"),
        &serde_json::to_vec_pretty(
            &json!({"database_dsn_file":dsn,"listen":format!("127.0.0.1:{factory_port}"),"forgejo_url":origin,"forgejo_internal_url":origin,"host_socket":root.join("absent-host.sock"),"identity_socket":root.join("absent-identity.sock"),"operator_id":1,"typesafe_api_key_file":root.join("absent-typesafe-key"),"factory_publication_root":root.join("factory-data")}),
        )?,
    )?;
    let mut factory = OwnedChild::spawn(
        Command::new(stage.join("bin/soda-factory-server"))
            .arg("--config")
            .arg(root.join("factory.json"))
            .arg("--extension-socket")
            .arg(root.join("factory.sock"))
            .env(
                "FORGEJO_EXTENSION_SERVICE_CALLBACK_SOCKET",
                root.join("callback.sock"),
            )
            .env(
                "FORGEJO_EXTENSION_SERVICE_CALLBACK_PEER_UID",
                uid.to_string(),
            ),
        &root.join("factory.log"),
    )?;
    let mut host = OwnedChild::spawn(&mut cli(&["web"]), &root.join("fountain.log"))?;
    private_file(&root.join("cookies"), b"")?;
    let mut browser = Browser {
        root: root.clone(),
        origin: origin.clone(),
        count: 0,
    };
    let until = Instant::now() + Duration::from_secs(45);
    loop {
        require(
            host.child.try_wait()?.is_none() && factory.child.try_wait()?.is_none(),
            "native host/Factory stopped during startup; inspect private profile logs",
        )?;
        if browser
            .request("/user/login", None, None)
            .is_ok_and(|r| r.0 == 200)
        {
            break;
        }
        require(
            Instant::now() < until,
            "native login readiness deadline exceeded",
        )?;
        thread::sleep(Duration::from_millis(250));
    }
    let api = "/-/native/pages/soda/feed/api/factory/";
    require(
        browser.request(&format!("{api}identity"), None, None)?.0 == 401,
        "anonymous native API was not refused",
    )?;
    let login_form = form(&[("user_name", "factory-check"), ("password", password)]);
    require(
        browser.request("/user/login", Some(&login_form), None)?.0 == 303,
        "native human login failed",
    )?;
    let until = Instant::now() + Duration::from_secs(20);
    let page = loop {
        let page = browser.request("/feed", None, None)?;
        if page.0 == 200 {
            break page;
        }
        require(
            Instant::now() < until,
            "current Soda extension page unavailable",
        )?;
        thread::sleep(Duration::from_millis(250));
    };
    let generation = page
        .2
        .lines()
        .find_map(|l| {
            l.split_once(':')
                .filter(|(k, _)| k.eq_ignore_ascii_case("X-Extension-Session-Generation"))
                .map(|(_, v)| v.trim().to_string())
        })
        .ok_or("native generation missing")?;
    let identity = browser.request(&format!("{api}identity"), None, Some(&generation))?;
    require(
        identity.0 == 200,
        "admitted real Factory identity read failed",
    )?;
    let identity: Value = serde_json::from_str(&identity.1)?;
    require(
        identity["native_actor"] == "1" && identity["host"].as_str().is_some_and(|s| s.len() == 32),
        "native human/canonical identity response invalid",
    )?;
    let catalogue = browser.request(
        &format!("{api}collaboration/repositories"),
        None,
        Some(&generation),
    )?;
    require(
        catalogue.0 == 200,
        "admitted real Factory catalogue read failed",
    )?;
    let catalogue: Value = serde_json::from_str(&catalogue.1)?;
    require(
        catalogue["observation"]["state"] == "available"
            && catalogue["items"].as_array().is_some_and(Vec::is_empty)
            && catalogue["complete"] == true,
        "native empty catalogue was not observed",
    )?;
    let issue_evidence = if issues {
        Some(fountain_read_issues::run(
            &mut browser,
            &generation,
            password,
            api,
        )?)
    } else {
        None
    };
    let issue_browser_evidence = if issue_browser {
        Some(fountain_issue_browser::run(&mut browser, &generation, api)?)
    } else {
        None
    };
    let pull_code_evidence = if pull_code {
        Some(fountain_read_pull_code::run(
            &mut browser,
            &generation,
            password,
            api,
        )?)
    } else {
        None
    };
    let pull_check_evidence = if pull_checks {
        Some(fountain_read_pull_checks::run(
            &mut browser,
            &generation,
            password,
            api,
        )?)
    } else {
        None
    };
    let read_edge_evidence = if read_edges {
        Some(fountain_read_edges::run(
            &mut browser,
            &generation,
            password,
            api,
        )?)
    } else {
        None
    };
    require(
        browser
            .request(&format!("{api}identity"), None, Some("stale-generation"))?
            .0
            == 409,
        "stale generation was not refused",
    )?;
    let retired_cookies = fs::read(root.join("cookies"))?;
    let logout = browser.request("/user/logout", Some(""), None)?;
    require(logout.0 == 200, "native logout failed")?;
    let logout: Value = serde_json::from_str(&logout.1)?;
    require(logout["redirect"] == "/", "native logout redirect invalid")?;
    // Replay the old private jar: clearing the browser's cookies alone would
    // not prove that the native server retired the authenticated session.
    fs::write(root.join("cookies"), retired_cookies)?;
    require(
        browser
            .request(&format!("{api}identity"), None, Some(&generation))?
            .0
            == 401,
        "native logout did not withdraw admission",
    )?;
    host.stop()?;
    factory.stop()?;
    private_file(
        &root.join("result.json"),
        &serde_json::to_vec_pretty(
            &json!({"result":"pass","origin":origin,"native_login":true,"separate_factory":true,"catalogue":"observed_empty_before_fixture","issues":issue_evidence,"issue_browser":issue_browser_evidence,"pull_code":pull_code_evidence,"pull_checks":pull_check_evidence,"read_edges":read_edge_evidence,"generation_refusal":true,"logout_refusal":true,"clean_shutdown":true}),
        )?,
    )?;
    println!("PASS: real native Fountain + installed Soda package + separate Factory, TLS human login, admitted identity/catalogue, stale generation/logout refusal and clean shutdown");
    Ok(())
}
fn main() {
    if let Err(error) = run() {
        eprintln!("Fountain host check: {error}");
        std::process::exit(1)
    }
}
