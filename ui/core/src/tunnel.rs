//! Desktop root-HTTP byte stream over the board's authenticated WebSocket.
use crate::{Config, Result};
use futures_util::{Sink, Stream};
use std::{
    io,
    pin::Pin,
    task::{Context, Poll, ready},
    time::Duration,
};
use tokio::{
    io::{AsyncRead, AsyncWrite, ReadBuf},
    net::TcpStream,
};
use tokio_tungstenite::{
    MaybeTlsStream, WebSocketStream, connect_async_with_config,
    tungstenite::{
        Bytes, Error, Message,
        client::IntoClientRequest,
        protocol::{WebSocketConfig, frame::coding::CloseCode},
    },
};

const PROTOCOL: &str = "ui.forwards.tunnel.v1";
const MESSAGE_LIMIT: usize = 64 * 1024;

/// Validated connection settings and native-only handshake credentials.
/// Clone for independent HTTP connections; no target address is accepted.
#[derive(Clone)]
pub struct ForwardConnector {
    origin: url::Url,
    authorization: Option<String>,
}
impl ForwardConnector {
    pub fn new(config: &Config, secret: &str) -> Result<Self> {
        let mut config = config.clone();
        config.validate(false)?;
        let authorization = config.authorization(secret)?;
        let mut origin = url::Url::parse(&config.origin).map_err(|_| "Invalid board origin.")?;
        let scheme = if origin.scheme() == "https" {
            "wss"
        } else {
            "ws"
        };
        origin
            .set_scheme(scheme)
            .map_err(|_| "Invalid board origin.")?;
        Ok(Self {
            origin,
            authorization,
        })
    }
    /// Open one HTTP/1.1 connection. Requires a Tokio runtime with I/O and time
    /// enabled. Caller discovers support with BoardClient::capabilities first.
    /// TLS verifies the board certificate; redirects are never followed.
    pub async fn connect(&self, forward_id: &str) -> Result<ForwardTunnel> {
        if forward_id.len() != 32
            || !forward_id
                .bytes()
                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        {
            return Err("Invalid registered forward ID.".into());
        }
        let mut url = self.origin.clone();
        url.set_path(&format!("/api/ui/forwards/{forward_id}/tunnel"));
        let mut request = url
            .as_str()
            .into_client_request()
            .map_err(|_| "Invalid tunnel request.")?;
        request
            .headers_mut()
            .insert("Sec-WebSocket-Protocol", PROTOCOL.parse().unwrap());
        if let Some(auth) = &self.authorization {
            request.headers_mut().insert(
                "Authorization",
                auth.parse().map_err(|_| "Invalid authentication header.")?,
            );
        }
        let config = WebSocketConfig::default()
            .read_buffer_size(16 * 1024)
            .write_buffer_size(0)
            .max_write_buffer_size(MESSAGE_LIMIT + 1024)
            .max_message_size(Some(MESSAGE_LIMIT))
            .max_frame_size(Some(MESSAGE_LIMIT));
        let (socket, response) = tokio::time::timeout(
            Duration::from_secs(20), connect_async_with_config(request, Some(config), true),
        ).await.map_err(|_| "Forward tunnel handshake timed out.")?
            .map_err(|error| match error {
                Error::Http(response) => format!("Forward tunnel refused (HTTP {}). Check authentication and whether the forward still exists.",response.status().as_u16()),
                _ => "Cannot open forward tunnel. Check the board address, TLS certificate and network.".into(),
            })?;
        if response
            .headers()
            .get("Sec-WebSocket-Protocol")
            .and_then(|h| h.to_str().ok())
            != Some(PROTOCOL)
        {
            return Err("Server did not negotiate ui.forwards.tunnel.v1.".into());
        }
        Ok(ForwardTunnel {
            socket: Some(socket),
            pending: Bytes::new(),
        })
    }
}

/// A bounded, backpressured byte stream implementing Tokio AsyncRead/AsyncWrite.
/// Send root HTTP/1.1, including chunked bodies and application WS handshakes.
/// `tokio::io::split` and `copy_bidirectional` can serve native consumers without
/// buffering complete requests/responses. Dropping closes the socket immediately;
/// shutdown closes both directions (there is no TCP half-close in this protocol).
pub struct ForwardTunnel {
    socket: Option<WebSocketStream<MaybeTlsStream<TcpStream>>>,
    pending: Bytes,
}
fn transport_error() -> io::Error {
    io::Error::other("Forward tunnel connection failed.")
}
fn closed_error() -> io::Error {
    io::Error::new(io::ErrorKind::BrokenPipe, "Forward tunnel is closed.")
}
impl AsyncRead for ForwardTunnel {
    fn poll_read(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        output: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        if output.remaining() == 0 {
            return Poll::Ready(Ok(()));
        }
        loop {
            if !self.pending.is_empty() {
                let n = output.remaining().min(self.pending.len());
                output.put_slice(&self.pending.split_to(n));
                return Poll::Ready(Ok(()));
            }
            let Some(socket) = self.socket.as_mut() else {
                return Poll::Ready(Ok(()));
            };
            match ready!(Pin::new(socket).poll_next(cx)) {
                Some(Ok(Message::Binary(data))) => self.pending = data,
                Some(Ok(Message::Ping(_) | Message::Pong(_))) => continue,
                Some(Ok(Message::Close(Some(frame))))
                    if !matches!(frame.code, CloseCode::Normal | CloseCode::Away) =>
                {
                    self.socket = None;
                    return Poll::Ready(Err(transport_error()));
                }
                None | Some(Ok(Message::Close(_))) => {
                    self.socket = None;
                    return Poll::Ready(Ok(()));
                }
                Some(Ok(_)) => {
                    self.socket = None;
                    return Poll::Ready(Err(io::Error::new(
                        io::ErrorKind::InvalidData,
                        "Forward tunnel requires binary messages.",
                    )));
                }
                Some(Err(_)) => {
                    self.socket = None;
                    return Poll::Ready(Err(transport_error()));
                }
            }
        }
    }
}
impl AsyncWrite for ForwardTunnel {
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        input: &[u8],
    ) -> Poll<io::Result<usize>> {
        if input.is_empty() {
            return Poll::Ready(Ok(0));
        }
        let Some(socket) = self.socket.as_mut() else {
            return Poll::Ready(Err(closed_error()));
        };
        // Finish a blocked write before accepting another bounded chunk.
        if ready!(Pin::new(&mut *socket).poll_ready(cx)).is_err() {
            self.socket = None;
            return Poll::Ready(Err(transport_error()));
        }
        let n = input.len().min(MESSAGE_LIMIT);
        if Pin::new(socket)
            .start_send(Message::Binary(Bytes::copy_from_slice(&input[..n])))
            .is_err()
        {
            self.socket = None;
            return Poll::Ready(Err(transport_error()));
        }
        Poll::Ready(Ok(n))
    }
    fn poll_flush(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        let Some(socket) = self.socket.as_mut() else {
            return Poll::Ready(Err(closed_error()));
        };
        if ready!(Pin::new(socket).poll_flush(cx)).is_err() {
            self.socket = None;
            return Poll::Ready(Err(transport_error()));
        }
        Poll::Ready(Ok(()))
    }
    fn poll_shutdown(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        let Some(socket) = self.socket.as_mut() else {
            return Poll::Ready(Ok(()));
        };
        let result = ready!(Pin::new(socket).poll_close(cx)).map_err(|_| transport_error());
        self.socket = None;
        Poll::Ready(result)
    }
}
