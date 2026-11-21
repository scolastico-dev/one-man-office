//! Connection protocol shared by the desktop shell and mobile scheduling adapters.
pub mod client;
pub mod config;
pub mod delivery;
mod saved;
pub use client::*;
pub use config::*;
pub use delivery::*;
pub use saved::*;
pub type Result<T> = std::result::Result<T, String>;

pub mod lifecycle;

pub mod local;
pub use local::*;

#[cfg(not(target_os = "android"))]
pub mod tunnel;
#[cfg(not(target_os = "android"))]
pub use tunnel::*;

#[cfg(not(target_os = "android"))]
pub mod forwards;
#[cfg(not(target_os = "android"))]
pub use forwards::*;
#[cfg(not(target_os = "android"))]
mod forward_listener;
#[cfg(not(target_os = "android"))]
pub use forward_listener::*;
