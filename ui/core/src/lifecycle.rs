//! Desktop window policy; ownership is established by the native local runtime,
//! never by the configured connection mode.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum CloseIntent {
    Main,
    LastSetup,
    ConnectedSetup,
    Child,
    Quit,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum CloseAction {
    CloseWindow,
    Hide,
    Exit,
    StopAndExit,
}
pub fn close_action(intent: CloseIntent, owned_live: bool, stop_with_window: bool) -> CloseAction {
    match intent {
        CloseIntent::ConnectedSetup | CloseIntent::Child => CloseAction::CloseWindow,
        _ if !owned_live => CloseAction::Exit,
        CloseIntent::Quit => CloseAction::StopAndExit,
        _ if stop_with_window => CloseAction::StopAndExit,
        _ => CloseAction::Hide,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    // Catch accidentally treating a local-mode attachment as process ownership,
    // or forgetting ownership after disconnecting/switching the board connection.
    #[test]
    fn live_ownership_keeps_the_app_available_after_main_or_last_setup_close() {
        for intent in [CloseIntent::Main, CloseIntent::LastSetup] {
            assert_eq!(close_action(intent, true, false), CloseAction::Hide);
            assert_eq!(close_action(intent, true, true), CloseAction::StopAndExit);
        }
    }
    #[test]
    fn unowned_or_lost_instances_never_keep_a_tray_or_receive_stop() {
        for intent in [CloseIntent::Main, CloseIntent::LastSetup, CloseIntent::Quit] {
            for setting in [false, true] {
                assert_eq!(close_action(intent, false, setting), CloseAction::Exit);
            }
        }
    }
    #[test]
    fn closing_connection_or_child_keeps_the_connected_board_running() {
        for intent in [CloseIntent::ConnectedSetup, CloseIntent::Child] {
            for owned in [false, true] {
                for setting in [false, true] {
                    assert_eq!(
                        close_action(intent, owned, setting),
                        CloseAction::CloseWindow
                    );
                }
            }
        }
    }
    #[test]
    fn explicit_quit_stops_owned_server_even_when_window_close_would_hide() {
        assert_eq!(
            close_action(CloseIntent::Quit, true, false),
            CloseAction::StopAndExit
        );
        assert_eq!(
            close_action(CloseIntent::Quit, true, true),
            CloseAction::StopAndExit
        );
    }
}
