// Compile-time unit-test ban (AGENTS.md): unit testing is bureaucracy.
// Logic lives in tools/unitban/build_impl.rs, shared by every member.
include!("../../tools/unitban/build_impl.rs");

fn main() {
    deny_unit_tests();
}
