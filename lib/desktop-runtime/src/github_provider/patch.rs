//! Retain incomplete provider hunk evidence without claiming an applyable patch.
//! Diffy owns syntax validation. Span/stat checks mirror the previous Go reader.
use serde_json::{json, Value};

pub(super) fn classify(
    raw: &str,
    additions: Option<u64>,
    deletions: Option<u64>,
) -> (&'static str, Value) {
    let Some((short, added, deleted)) = spans(raw) else {
        return ("unavailable", Value::Null);
    };
    let (Some(want_added), Some(want_deleted)) = (additions, deletions) else {
        return ("unavailable", Value::Null);
    };
    if added > want_added || deleted > want_deleted {
        return ("unavailable", Value::Null);
    }
    (
        if short || added < want_added || deleted < want_deleted {
            "truncated"
        } else {
            "complete"
        },
        json!(raw),
    )
}
fn range(value: &str, prefix: char) -> Option<(usize, usize)> {
    let value = value.strip_prefix(prefix)?;
    let (start, count) = value.split_once(',').unwrap_or((value, "1"));
    let start = start.parse::<usize>().ok()?;
    let count = count.parse::<usize>().ok()?;
    start.checked_add(count)?;
    Some((start, count))
}
fn spans(raw: &str) -> Option<(bool, u64, u64)> {
    let lines: Vec<_> = raw.split_inclusive('\n').collect();
    let (mut at, mut short, mut added, mut deleted) = (0, false, 0, 0);
    let mut syntax = String::from("--- before\n+++ after\n");
    while at < lines.len() {
        let header = lines[at].strip_prefix("@@ ")?;
        let (ranges, hint) = header.split_once(" @@")?;
        let mut ranges = ranges.split_whitespace();
        let (old_start, old_count) = range(ranges.next()?, '-')?;
        let (new_start, new_count) = range(ranges.next()?, '+')?;
        if ranges.next().is_some() {
            return None;
        }
        at += 1;
        let start = at;
        let (mut old, mut new) = (0, 0);
        while at < lines.len() && !lines[at].starts_with("@@ ") {
            match lines[at].as_bytes().first()? {
                b' ' => {
                    old += 1;
                    new += 1;
                }
                b'+' => {
                    added += 1;
                    new += 1;
                }
                b'-' => {
                    deleted += 1;
                    old += 1;
                }
                b'\\'
                    if lines[at].trim_end_matches(['\r', '\n'])
                        == "\\ No newline at end of file" => {}
                _ => return None,
            }
            at += 1;
        }
        if old > old_count || new > new_count || old + new == 0 {
            return None;
        }
        short |= old < old_count || new < new_count;
        // Validate observed lines with their observed spans, so a short final
        // provider hunk remains displayable as truncated. Return the original
        // provider fragment, never this syntax-only validation representation.
        syntax.push_str(&format!(
            "@@ -{old_start},{old} +{new_start},{new} @@{}\n",
            hint.trim_end_matches(['\r', '\n'])
        ));
        for line in &lines[start..at] {
            syntax.push_str(line);
        }
        if !syntax.ends_with('\n') {
            syntax.push('\n');
        }
    }
    if at == 0 || diffy::Patch::from_str(&syntax).ok()?.hunks().is_empty() {
        return None;
    }
    Some((short, added, deleted))
}
