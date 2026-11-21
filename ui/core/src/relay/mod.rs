//! Native version-1 relay connection. Forwarded content gets no access to this API.
//! The caller owns explicit new-pair authorization and reconnect policy.
mod connection;
pub mod crypto;
mod state;
pub mod wire;
pub use connection::{
    Pairing, PairingInfo, RecordReceiver, RecordSender, RelaySession, SessionControl, SessionStatus,
};
pub use state::{RelayClient, RelayStatus, SecretStore, TrustState};
