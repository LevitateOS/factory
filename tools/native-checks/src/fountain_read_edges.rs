//! Remaining installed provider cases: large/literal diffs and PR namespaces.
use super::fountain_read_issues::{available, curl_string, read, Seed};
use super::fountain_read_pull_code::pages;
use super::{form, private_file, require, Browser, Result};
use base64::{engine::general_purpose::STANDARD, Engine};
use serde_json::{json, Value};

fn all_files(
    b: &mut Browser,
    prefix: &str,
    generation: &str,
) -> Result<(Value, Vec<Value>, String)> {
    let first = read(b, &format!("{prefix}/files?limit=1"), generation, 200)?;
    let diff = first["diff"].clone();
    let base = diff["base"].as_str().ok_or("diff base missing")?;
    let head = diff["head"].as_str().ok_or("diff head missing")?;
    let first_cursor = first["files"]["next"]
        .as_str()
        .ok_or("first file cursor missing")?
        .to_owned();
    let mut page = first;
    let mut files: Vec<Value> = vec![];
    let mut cursor = String::new();
    for _ in 0..10 {
        require(page["diff"] == diff, "diff pair changed while paging")?;
        available(&page["files"])?;
        let rows = page["files"]["items"]
            .as_array()
            .ok_or("file items missing")?;
        require(rows.len() <= 1, "file page exceeded limit")?;
        for row in rows {
            require(
                row["diff"] == diff
                    && !files.iter().any(|f| {
                        f["old_path"] == row["old_path"] && f["new_path"] == row["new_path"]
                    }),
                "file identity repeated across pages",
            )?;
            files.push(row.clone());
        }
        if page["files"]["complete"] == true {
            require(
                page["files"]["next"] == "",
                "complete file list retained cursor",
            )?;
            return Ok((diff, files, first_cursor));
        }
        let next = page["files"]["next"]
            .as_str()
            .ok_or("file continuation missing")?;
        require(
            !next.is_empty() && next != cursor,
            "file continuation did not advance",
        )?;
        cursor = next.to_owned();
        page = read(
            b,
            &format!(
                "{prefix}/files?limit=1&{}",
                form(&[("base", base), ("head", head), ("cursor", &cursor)])
            ),
            generation,
            200,
        )?;
    }
    Err("file paging did not finish".into())
}

pub(super) fn run(b: &mut Browser, generation: &str, password: &str, api: &str) -> Result<Value> {
    private_file(
        &b.root.join("seed.curl"),
        format!("user = \"factory-check:{}\"\n", curl_string(password)).as_bytes(),
    )?;
    let first = Seed { browser: b }.json("POST", "/api/v1/user/repos", Some(json!({"name":"edges-one","private":true,"auto_init":true,"default_branch":"main","readme":"Default"})), 201)?;
    let second = Seed { browser: b }.json("POST", "/api/v1/user/repos", Some(json!({"name":"edges-two","private":true,"auto_init":true,"default_branch":"main","readme":"Default"})), 201)?;
    let first_api = "/api/v1/repos/factory-check/edges-one";
    let second_api = "/api/v1/repos/factory-check/edges-two";
    let old_text = "stable rename content\n".repeat(200);
    Seed { browser: b }.json("POST", &format!("{first_api}/contents"), Some(json!({"branch":"main","message":"native base files","files":[{"operation":"create","path":"old.txt","content":STANDARD.encode(&old_text)},{"operation":"create","path":"delete.txt","content":STANDARD.encode(b"delete me\n")},{"operation":"create","path":"modify.txt","content":STANDARD.encode(b"before\n")}]})), 201)?;
    let mut blobs = vec![];
    for name in ["old.txt", "delete.txt", "modify.txt"] {
        let blob = Seed { browser: b }.json(
            "GET",
            &format!("{first_api}/contents/{name}?ref=main"),
            None,
            200,
        )?;
        blobs.push(
            blob["sha"]
                .as_str()
                .ok_or("native base blob missing")?
                .to_owned(),
        );
    }
    let large: String = (0..7000)
        .map(|line| format!("native line {line:04} 界\n"))
        .collect();
    let literal = ":(glob)literal-名前[1].txt";
    Seed { browser: b }.json("POST", &format!("{first_api}/contents"), Some(json!({"branch":"main","new_branch":"feature","message":"native changed-file coverage","files":[{"operation":"update","path":"renamed-名前.txt","from_path":"old.txt","sha":blobs[0],"content":STANDARD.encode(&old_text)},{"operation":"delete","path":"delete.txt","sha":blobs[1]},{"operation":"update","path":"modify.txt","sha":blobs[2],"content":STANDARD.encode(b"after\n")},{"operation":"create","path":"large.txt","content":STANDARD.encode(&large)},{"operation":"create","path":"blob.bin","content":STANDARD.encode(b"\0real binary\0\xff")},{"operation":"create","path":literal,"content":STANDARD.encode("literal Unicode path\n")}]})), 201)?;
    let pull = Seed { browser: b }.json("POST", &format!("{first_api}/pulls"), Some(json!({"base":"main","head":"feature","title":"First native PR number one","body":"first repository PR body"})), 201)?;
    Seed { browser: b }.json("POST", &format!("{second_api}/contents/other.txt"), Some(json!({"branch":"main","new_branch":"feature","content":STANDARD.encode(b"other repository change\n")})), 201)?;
    let other = Seed { browser: b }.json("POST", &format!("{second_api}/pulls"), Some(json!({"base":"main","head":"feature","title":"Second native PR number one","body":"other repository PR body"})), 201)?;
    require(
        pull["number"] == 1 && other["number"] == 1 && pull["id"] != other["id"],
        "same-number PR fixture did not separate native IDs",
    )?;
    let repos = pages(
        b,
        &format!("{api}collaboration/repositories?query=edges-"),
        generation,
        "id",
    )?;
    require(
        repos.len() == 2,
        "two private edge repositories not discovered",
    )?;
    let lookup = |native: &Value| -> Result<String> {
        let external = native["id"]
            .as_i64()
            .ok_or("native repository ID missing")?
            .to_string();
        Ok(repos
            .iter()
            .find(|r| r["external_id"] == external)
            .and_then(|r| r["id"].as_str())
            .ok_or("canonical edge repository missing")?
            .to_owned())
    };
    let id = lookup(&first)?;
    let other_id = lookup(&second)?;
    require(id != other_id, "canonical repository collision")?;
    let repository = format!("{api}collaboration/repositories/{id}");
    let other_repository = format!("{api}collaboration/repositories/{other_id}");
    let prefix = format!("{repository}/pulls/1");
    let other_prefix = format!("{other_repository}/pulls/1");
    let detail = read(b, &prefix, generation, 200)?;
    let other_detail = read(b, &other_prefix, generation, 200)?;
    require(
        detail["ref"]["number"] == "1"
            && other_detail["ref"]["number"] == "1"
            && detail["ref"]["external_id"]
                == pull["id"]
                    .as_i64()
                    .ok_or("first PR ID missing")?
                    .to_string()
            && other_detail["ref"]["external_id"]
                == other["id"]
                    .as_i64()
                    .ok_or("second PR ID missing")?
                    .to_string()
            && detail["ref"]["repository"] == id
            && other_detail["ref"]["repository"] == other_id
            && detail["body"] == "first repository PR body"
            && other_detail["body"] == "other repository PR body",
        "same-number PR body or typed scope crossed repositories",
    )?;
    read(b, &format!("{repository}/issues/1"), generation, 404)?;
    let issues = read(b, &format!("{repository}/issues?limit=1"), generation, 200)?;
    require(
        issues["items"] == json!([]) && issues["complete"] == true,
        "PR leaked into ordinary issue list",
    )?;
    let (diff, files, cursor) = all_files(b, &prefix, generation)?;
    require(
        diff["pull"] == detail["ref"]
            && diff["base"] == pull["merge_base"]
            && diff["head"] == pull["head"]["sha"]
            && files.len() == 6,
        "actual edge diff pair or complete file catalogue lost",
    )?;
    let find = |name: &str| -> Result<&Value> {
        files
            .iter()
            .find(|f| f["new_path"] == name || f["old_path"] == name)
            .ok_or_else(|| format!("native changed file {name} missing").into())
    };
    let large_file = find("large.txt")?;
    let patch = large_file["patch"]
        .as_str()
        .ok_or("truncated text patch missing")?;
    require(
        large_file["patch_state"] == "truncated"
            && patch.len() <= 16384
            && patch.len() > 16000
            && patch.contains("界")
            && large_file["additions"] == 7000
            && large_file["deletions"] == 0,
        "truncated Unicode patch or exact full-file counts lost",
    )?;
    let binary = find("blob.bin")?;
    require(
        binary["patch_state"] == "binary"
            && binary["patch"].is_null()
            && binary["additions"].is_null()
            && binary["deletions"].is_null(),
        "binary patch/counts were invented",
    )?;
    let renamed = find("old.txt")?;
    require(
        renamed["old_path"] == "old.txt"
            && renamed["new_path"] == "renamed-名前.txt"
            && renamed["change"] == "renamed"
            && renamed["additions"] == 0
            && renamed["deletions"] == 0,
        "native rename pair/counts lost",
    )?;
    let modified = find("modify.txt")?;
    require(
        modified["change"] == "modified"
            && modified["additions"] == 1
            && modified["deletions"] == 1
            && modified["patch"]
                .as_str()
                .is_some_and(|s| s.contains("-before") && s.contains("+after")),
        "native modified patch lost",
    )?;
    let deleted = find("delete.txt")?;
    require(
        deleted["change"] == "deleted"
            && deleted["new_path"] == ""
            && deleted["deletions"] == 1
            && find(literal)?["patch_state"] == "complete",
        "deleted or literal filename facts lost",
    )?;
    let (other_diff, other_files, _) = all_files(b, &other_prefix, generation)?;
    require(
        other_files.len() == 1
            && other_files[0]["new_path"] == "other.txt"
            && other_diff["pull"] == other_detail["ref"],
        "other repository file scope crossed",
    )?;
    let other_base = other_diff["base"]
        .as_str()
        .ok_or("other diff base missing")?;
    let other_head = other_diff["head"]
        .as_str()
        .ok_or("other diff head missing")?;
    read(
        b,
        &format!(
            "{other_prefix}/files?limit=1&{}",
            form(&[
                ("base", other_base),
                ("head", other_head),
                ("cursor", &cursor)
            ])
        ),
        generation,
        400,
    )?;
    let caps = read(b, &format!("{prefix}/capabilities"), generation, 200)?;
    let other_caps = read(b, &format!("{other_prefix}/capabilities"), generation, 200)?;
    require(
        caps["pull"] == detail["ref"]
            && other_caps["pull"] == other_detail["ref"]
            && caps["repository"] == id
            && other_caps["repository"] == other_id,
        "capability parent scope crossed same-number PRs",
    )?;
    Seed { browser: b }.json(
        "PATCH",
        first_api,
        Some(json!({"name":"edges-renamed"})),
        200,
    )?;
    let renamed_repos = pages(
        b,
        &format!("{api}collaboration/repositories?query=edges-"),
        generation,
        "id",
    )?;
    require(
        renamed_repos
            .iter()
            .any(|r| r["id"] == id && r["name"] == "factory-check/edges-renamed"),
        "native rename changed canonical repository",
    )?;
    let (after_diff, after_files, _) = all_files(b, &prefix, generation)?;
    require(
        after_diff == diff
            && after_files == files
            && read(b, &prefix, generation, 200)?["ref"] == detail["ref"],
        "Git/repository rename changed immutable PR/files",
    )?;
    let renamed_api = "/api/v1/repos/factory-check/edges-renamed";
    Seed { browser: b }.json(
        "PATCH",
        renamed_api,
        Some(json!({"has_pull_requests":false})),
        200,
    )?;
    for path in [
        &prefix,
        &format!("{prefix}/files"),
        &format!("{prefix}/checks"),
        &format!("{prefix}/capabilities"),
    ] {
        read(b, path, generation, 404)?;
    }
    let disabled = read(b, &format!("{repository}/capabilities"), generation, 200)?;
    require(
        disabled["actions"]["pulls.read"]["state"] == "unsupported",
        "disabled PR unit became an available empty view",
    )?;
    Seed { browser: b }.json(
        "PATCH",
        renamed_api,
        Some(json!({"has_pull_requests":true})),
        200,
    )?;
    require(
        read(b, &prefix, generation, 200)?["ref"] == detail["ref"],
        "PR identity changed across unit restoration",
    )?;
    Seed { browser: b }.json("PATCH", renamed_api, Some(json!({"archived":true})), 200)?;
    let archived = read(b, &format!("{prefix}/capabilities"), generation, 200)?;
    require(
        archived["actions"]["pull.edit"]["state"] == "denied"
            && archived["actions"]["pull.comment"]["state"] == "denied"
            && archived["actions"]["pull.merge"]["state"] == "denied"
            && read(b, &format!("{prefix}/files?limit=1"), generation, 200)?["diff"] == diff,
        "archive removed read facts or allowed author mutation",
    )?;
    println!("PASS: installed native same-number PR isolation, complete six-file catalogue with truncated Unicode/binary/literal/rename/delete/modified diffs, cross-repository cursor refusal, rename stability, unit withdrawal and archive capabilities");
    Ok(
        json!({"result":"pass","repositories":2,"pulls":2,"files":6,"large_file_lines":7000,"truncated_patch_bytes":patch.len(),"same_number_isolation":true,"cross_repository_cursor":true,"rename":true,"unit_withdrawal":true,"archive":true}),
    )
}
