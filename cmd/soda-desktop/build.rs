include!("../../tools/unitban/build_impl.rs");
fn main() {
    deny_unit_tests();
    #[cfg(feature = "native-ui")]
    tauri_build::build();
}
