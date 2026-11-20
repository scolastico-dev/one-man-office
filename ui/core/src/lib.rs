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
