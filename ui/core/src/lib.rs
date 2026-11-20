//! Connection protocol shared by the desktop shell and mobile scheduling adapters.
pub mod client;
pub mod config;
pub mod delivery;
pub use client::*;
pub use config::*;
pub use delivery::*;
pub type Result<T> = std::result::Result<T, String>;
