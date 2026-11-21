//! Keep the native Host content title/frame even when Tauri maps HTML fullscreen
//! to a borderless OS window. This applies to main and child WebViews.
use omo_ui_core::Result;
use windows_sys::Win32::{
    Foundation::*,
    UI::{Shell::*, WindowsAndMessaging::*},
};
const SUBCLASS: usize = 0x4f4d4f;

pub fn install(hwnd: HWND) -> Result<()> {
    // Called on the owning window's UI thread, before navigating host content.
    if unsafe { SetWindowSubclass(hwnd, Some(frame), SUBCLASS, 0) } == 0 {
        return Err("Cannot protect the native host window frame.".into());
    }
    Ok(())
}
unsafe extern "system" fn frame(
    hwnd: HWND,
    message: u32,
    wparam: WPARAM,
    lparam: LPARAM,
    _id: usize,
    _data: usize,
) -> LRESULT {
    unsafe {
        match message {
            WM_STYLECHANGING if wparam as i32 == GWL_STYLE && lparam != 0 => {
                let styles = &mut *(lparam as *mut STYLESTRUCT);
                styles.styleNew |= WS_CAPTION | WS_SYSMENU | WS_THICKFRAME;
            }
            // Tao's fullscreen bookkeeping may otherwise remove the nonclient
            // area. Always let Windows calculate our enforced native frame.
            WM_NCCALCSIZE => return DefWindowProcW(hwnd, message, wparam, lparam),
            WM_NCDESTROY => {
                RemoveWindowSubclass(hwnd, Some(frame), SUBCLASS);
            }
            _ => {}
        }
        DefSubclassProc(hwnd, message, wparam, lparam)
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn a_borderless_style_request_keeps_the_native_caption_and_nonclient_area() {
        unsafe {
            let hwnd: HWND = CreateWindowExW(
                0,
                windows_sys::core::w!("STATIC"),
                windows_sys::core::w!("Host content"),
                WS_OVERLAPPEDWINDOW,
                0,
                0,
                640,
                480,
                std::ptr::null_mut(),
                std::ptr::null_mut(),
                std::ptr::null_mut(),
                std::ptr::null(),
            );
            assert!(!hwnd.is_null());
            install(hwnd).unwrap();
            SetWindowLongW(hwnd, GWL_STYLE, WS_POPUP as i32);
            let style = GetWindowLongW(hwnd, GWL_STYLE) as u32;
            assert_eq!(style & WS_CAPTION, WS_CAPTION);
            assert_eq!(style & WS_THICKFRAME, WS_THICKFRAME);
            let mut area = RECT {
                left: 0,
                top: 0,
                right: 640,
                bottom: 480,
            };
            SendMessageW(hwnd, WM_NCCALCSIZE, 0, &mut area as *mut _ as isize);
            assert!(area.top > 0, "Host content must retain a native title area");
            DestroyWindow(hwnd);
        }
    }
}
