// Shared compile-time unit-test ban (AGENTS.md): unit testing is bureaucracy.
// Included by every workspace member's build script; integration tests live
// in crate-top tests/ only. Fails the build if unit-test markers appear
// under the including crate's src/.
fn unitban_visit(dir: &std::path::Path, under_tests: bool, bad: &mut Vec<String>) {
    let Ok(entries) = std::fs::read_dir(dir) else {
        return;
    };
    for entry in entries.flatten() {
        let path = entry.path();
        if path.is_dir() {
            let name = path
                .file_name()
                .map(|n| n.to_string_lossy())
                .unwrap_or_default();
            if name == "tests" {
                bad.push(format!("{}: test-only directory", path.display()));
            }
            unitban_visit(&path, under_tests || name == "tests", bad);
            continue;
        }
        if path.extension().is_none_or(|ext| ext != "rs") {
            continue;
        }
        if under_tests {
            bad.push(format!(
                "{}: file under test-only directory",
                path.display()
            ));
            continue;
        }
        let name = path
            .file_name()
            .map(|n| n.to_string_lossy())
            .unwrap_or_default();
        if name == "tests.rs" || name.ends_with("_tests.rs") {
            bad.push(format!("{}: test-only module", path.display()));
            continue;
        }
        let Ok(text) = std::fs::read_to_string(&path) else {
            continue;
        };
        for (n, line) in text.lines().enumerate() {
            let code = line.trim_start();
            if code.starts_with("//") {
                continue;
            }
            if code.contains("#[test]") || code.contains("cfg(test") {
                bad.push(format!("{}:{}: {}", path.display(), n + 1, code.trim()));
            }
        }
    }
}

fn deny_unit_tests() {
    println!("cargo:rerun-if-changed=src");
    println!("cargo:rerun-if-changed=../../tools/unitban/build_impl.rs");
    let mut bad = Vec::new();
    unitban_visit(std::path::Path::new("src"), false, &mut bad);
    assert!(
        bad.is_empty(),
        "unit tests are banned (AGENTS.md; why: docs/development/why-no-unit-tests.md); integration tests live in tests/:\n{}",
        bad.join("\n")
    );
}
