//! Complete paired discussion collection under one authenticated read. Leaf
//! readers retain their own brackets; this owner brackets their page decoders.
use super::{reads::Reader, records as r, Failure, Read, Result, ThreadRead};
use serde_json::{json, Value};
use std::collections::HashSet;

fn consume(remaining: &mut usize, items: usize) -> Result<()> {
    *remaining = remaining
        .checked_sub(items)
        .ok_or_else(|| Failure::new(413, "github_delivery_discussion_too_large"))?;
    Ok(())
}

impl Reader<'_> {
    pub(super) async fn delivery_discussion(
        &self,
        number: u32,
        head: &str,
        base: &str,
        account: &str,
    ) -> Result<Value> {
        let first = self
            .delivery_discussion_pages(number, head, base, account)
            .await?;
        self.recheck().await?;
        if self
            .delivery_discussion_pages(number, head, base, account)
            .await?
            != first
        {
            return Err(Failure::new(409, "github_delivery_discussion_changed"));
        }
        self.recheck().await?;
        Ok(json!({"pages": first}))
    }

    async fn delivery_discussion_pages(
        &self,
        number: u32,
        head: &str,
        base: &str,
        account: &str,
    ) -> Result<Vec<Value>> {
        let pull = self.raw_subject(true, number.into()).await?;
        if pull["head"]["sha"] != head || pull["base"]["sha"] != base {
            return Err(Failure::new(409, "github_delivery_discussion_changed"));
        }
        let subject = json!({"kind":"pull","external_id":r::id(&pull["id"])? ,"number":number});
        let mut pages = vec![
            json!({"query":Read::Pull{repository:self.repository.into(),number},
            "payload":{"repository":self.repository,"name":self.name,"items":[r::pull(&pull)?],"next_page":0}}),
        ];
        let mut remaining = 256;
        for comments in [true, false] {
            let mut page = 1;
            loop {
                let query = if comments {
                    Read::PullComments {
                        repository: self.repository.into(),
                        number,
                        page,
                        limit: 50,
                    }
                } else {
                    Read::PullReviews {
                        repository: self.repository.into(),
                        number,
                        page,
                        limit: 50,
                    }
                };
                let payload = if comments {
                    self.comments_for_subject(&query.value(), &subject).await?
                } else {
                    self.reviews_for_subject(&query.value(), false, &subject)
                        .await?
                };
                let items = payload["items"].as_array().ok_or_else(Failure::protocol)?;
                consume(&mut remaining, items.len())?;
                let next = payload["next_page"]
                    .as_u64()
                    .ok_or_else(Failure::protocol)?;
                if next != 0 && (items.is_empty() || next != u64::from(page) + 1) {
                    return Err(Failure::protocol());
                }
                pages.push(json!({"query":query,"payload":payload}));
                if next == 0 {
                    break;
                }
                page += 1;
            }
        }
        let mut page = 1;
        let mut cursor = String::new();
        let mut cursors = HashSet::new();
        loop {
            let query = ThreadRead {
                repository: self.repository.into(),
                number: number.into(),
                page,
                limit: 20,
                thread_id: String::new(),
            };
            let payload = self
                .graph_threads_for_pull(&query, &cursor, account, &pull, &self.repository_snapshot)
                .await?;
            let items = payload["items"].as_array().ok_or_else(Failure::protocol)?;
            consume(&mut remaining, items.len())?;
            for thread in items {
                let members = thread["comments"]["items"]
                    .as_array()
                    .ok_or_else(Failure::protocol)?;
                consume(&mut remaining, members.len())?;
                let mut next = thread["comments"]["next_page"]
                    .as_u64()
                    .ok_or_else(Failure::protocol)?;
                let mut after = r::text(&thread["comments"], "next_cursor")?.to_owned();
                let root = r::text(thread, "external_id")?;
                let mut member_cursors = HashSet::new();
                while next != 0 {
                    if next > u32::MAX.into() || !member_cursors.insert(after.clone()) {
                        return Err(Failure::protocol());
                    }
                    let detail = ThreadRead {
                        repository: self.repository.into(),
                        number: number.into(),
                        page: next as u32,
                        limit: 10,
                        thread_id: root.into(),
                    };
                    let part = self
                        .graph_threads_for_pull(
                            &detail,
                            &after,
                            account,
                            &pull,
                            &self.repository_snapshot,
                        )
                        .await?;
                    let detail_items = part["items"]
                        .as_array()
                        .filter(|items| items.len() == 1)
                        .ok_or_else(Failure::protocol)?;
                    let members = detail_items[0]["comments"]["items"]
                        .as_array()
                        .ok_or_else(Failure::protocol)?;
                    consume(&mut remaining, members.len())?;
                    pages.push(json!({"query":Read::PullThreads{repository:self.repository.into(),number,page:next as u32,limit:10,thread_id:root.into(),cursor:after},"payload":part}));
                    let next_members = &pages.last().ok_or_else(Failure::protocol)?["payload"]
                        ["items"][0]["comments"];
                    let following = next_members["next_page"]
                        .as_u64()
                        .ok_or_else(Failure::protocol)?;
                    if following != 0 && (members.is_empty() || following != next + 1) {
                        return Err(Failure::protocol());
                    }
                    next = following;
                    after = r::text(next_members, "next_cursor")?.to_owned();
                }
            }
            let next = payload["next_page"]
                .as_u64()
                .ok_or_else(Failure::protocol)?;
            let after = r::text(&payload, "next_cursor")?.to_owned();
            if next != 0
                && (items.is_empty()
                    || next != u64::from(page) + 1
                    || !cursors.insert(after.clone()))
            {
                return Err(Failure::protocol());
            }
            pages.push(json!({"query":Read::PullThreads{repository:self.repository.into(),number,page,limit:20,thread_id:String::new(),cursor},"payload":payload}));
            if next == 0 {
                break;
            }
            page += 1;
            cursor = after;
        }
        Ok(pages)
    }
}
