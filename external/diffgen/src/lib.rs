extern crate similar;

use similar::TextDiff;
use std::ffi::{CStr, CString};

#[no_mangle]
pub extern "C" fn diff(
    before: *const libc::c_char,
    after: *const libc::c_char,
) -> *mut libc::c_char {
    let before_cstr = unsafe { CStr::from_ptr(before) };
    let before_str = before_cstr.to_str().unwrap();

    let after_cstr = unsafe { CStr::from_ptr(after) };
    let after_str = after_cstr.to_str().unwrap();

    let diff = TextDiff::from_lines(before_str, after_str);

    CString::new(diff.unified_diff().to_string())
        .unwrap()
        .into_raw()
}

#[cfg(test)]
pub mod test {
    use super::*;
    use std::ffi::CString;

    #[test]
    fn test_diff() {
        let diff_result = diff(
            CString::new("hello\nworld\n").unwrap().into_raw(),
            CString::new("bye\nworld\n").unwrap().into_raw(),
        );

        assert_eq!(
            unsafe { CStr::from_ptr(diff_result) }.to_str().unwrap(),
            "@@ -1,2 +1,2 @@\n-hello\n+bye\n world\n"
        )
    }
}
