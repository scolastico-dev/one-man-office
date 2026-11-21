//! Native version-1 relay connection. Forwarded content gets no access to this API.
//! The caller owns explicit new-pair authorization and reconnect policy.
mod bridge;
mod connection;
pub mod crypto;
mod state;
mod streams;
pub mod wire;
pub use bridge::{BoardEndpoint, BoardTransport, TransportStatus};
pub use connection::{
    Pairing, PairingInfo, RecordReceiver, RecordSender, RelaySession, SessionControl, SessionStatus,
};
pub use state::{RelayClient, RelayStatus, SecretStore, TrustState};
pub use streams::{RelayStream, StreamMux};
