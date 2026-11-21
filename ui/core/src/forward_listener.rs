//! Scoped SOCKS routing and local-port exposure. Never dials an app directly.
use crate::{ForwardConnector, Result};
use std::{
    net::{Ipv4Addr, SocketAddr},
    sync::{Arc, Mutex},
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream},
    sync::oneshot,
    task::{JoinHandle, JoinSet},
};

pub struct ForwardListener {
    address: SocketAddr,
    error: Arc<Mutex<String>>,
    stop: Option<oneshot::Sender<()>>,
    worker: Option<JoinHandle<()>>,
}
impl ForwardListener {
    /// Port zero asks the OS for a private candidate; user input uses `local_port`.
    pub async fn local(connector: ForwardConnector, id: String, port: u16) -> Result<Self> {
        Self::start(connector, id, port, None).await
    }
    pub async fn preview(connector: ForwardConnector, id: String, host: String) -> Result<Self> {
        Self::start(connector, id, 0, Some(host)).await
    }
    async fn start(
        connector: ForwardConnector,
        id: String,
        port: u16,
        host: Option<String>,
    ) -> Result<Self> {
        let listener = TcpListener::bind((Ipv4Addr::LOCALHOST, port))
            .await
            .map_err(|_| {
                format!("Cannot listen on 127.0.0.1:{port}. Choose an available local port.")
            })?;
        let address = listener
            .local_addr()
            .map_err(|_| "Cannot read the local address.")?;
        let error = Arc::new(Mutex::new(String::new()));
        let errors = error.clone();
        let (stop, mut stopped) = oneshot::channel();
        let worker = tokio::spawn(async move {
            let mut clients = JoinSet::new();
            loop {
                tokio::select! {
                    biased;
                    _ = &mut stopped => break,
                    Some(_) = clients.join_next(), if !clients.is_empty() => {},
                    accepted = listener.accept(), if clients.len() < 128 => {
                        let (tcp, _) = match accepted {
                            Ok(value) => value,
                            Err(_) => { *errors.lock().expect("error lock") = "Local listener failed.".into(); break; }
                        };
                        let (connector, id, host, errors) = (connector.clone(), id.clone(), host.clone(), errors.clone());
                        clients.spawn(async move {
                            if let Err(e) = serve(tcp, connector, &id, host.as_deref(), &errors).await {
                                *errors.lock().expect("error lock") = e;
                            }
                        });
                    }
                }
            }
            // Completion means the listening socket AND every in-flight tunnel are gone.
            drop(listener);
            clients.shutdown().await;
        });
        Ok(Self {
            address,
            error,
            stop: Some(stop),
            worker: Some(worker),
        })
    }
    pub fn address(&self) -> SocketAddr {
        self.address
    }
    pub fn error(&self) -> String {
        self.error.lock().expect("error lock").clone()
    }
    pub async fn stop(mut self) -> Result<()> {
        self.stop.take();
        if let Some(worker) = self.worker.take() {
            worker
                .await
                .map_err(|_| "Cannot finish local listener shutdown.")?;
        }
        Ok(())
    }
}
impl Drop for ForwardListener {
    fn drop(&mut self) {
        self.stop.take();
    }
}
async fn serve(
    mut tcp: TcpStream,
    connector: ForwardConnector,
    id: &str,
    host: Option<&str>,
    errors: &Mutex<String>,
) -> Result<()> {
    if let Some(host) = host {
        tokio::time::timeout(
            std::time::Duration::from_secs(20),
            socks_request(&mut tcp, host),
        )
        .await
        .map_err(|_| "Preview connection setup expired.")??;
    }
    let mut tunnel = match connector.connect(id).await {
        Ok(tunnel) => tunnel,
        Err(error) => {
            *errors.lock().expect("error lock") = error.clone();
            if host.is_some() {
                let _ = tcp.write_all(&[5, 1, 0, 1, 0, 0, 0, 0, 0, 0]).await;
            } else {
                let body = format!("Cannot connect to the registered app. {error}\n");
                let reply = format!(
                    "HTTP/1.1 502 Bad Gateway\r\nContent-Type: text/plain; charset=utf-8\r\nCache-Control: no-store\r\nConnection: close\r\nContent-Length: {}\r\n\r\n{body}",
                    body.len()
                );
                let _ = tcp.write_all(reply.as_bytes()).await;
            }
            return Err(error);
        }
    };
    if host.is_some() {
        tcp.write_all(&[5, 0, 0, 1, 0, 0, 0, 0, 0, 0])
            .await
            .map_err(|_| "Preview client disconnected.")?;
    }
    *errors.lock().expect("error lock") = String::new();
    tokio::io::copy_bidirectional(&mut tcp, &mut tunnel)
        .await
        .map_err(
            |_| "The app connection ended unexpectedly. Check omo and the app, then reconnect.",
        )?;
    Ok(())
}
async fn socks_request(tcp: &mut TcpStream, host: &str) -> Result<()> {
    async fn request(tcp: &mut TcpStream, host: &str) -> std::io::Result<bool> {
        let mut greeting = [0; 2];
        tcp.read_exact(&mut greeting).await?;
        if greeting[0] != 5 {
            return Ok(false);
        }
        let mut methods = vec![0; greeting[1] as usize];
        tcp.read_exact(&mut methods).await?;
        if !methods.contains(&0) {
            tcp.write_all(&[5, 255]).await?;
            return Ok(false);
        }
        tcp.write_all(&[5, 0]).await?;
        let mut header = [0; 4];
        tcp.read_exact(&mut header).await?;
        if header != [5, 1, 0, 3] {
            return Ok(false);
        }
        let len = tcp.read_u8().await? as usize;
        let mut name = vec![0; len];
        tcp.read_exact(&mut name).await?;
        let port = tcp.read_u16().await?;
        Ok(name.eq_ignore_ascii_case(host.as_bytes()) && port == 80)
    }
    if request(tcp, host).await.unwrap_or(false) {
        return Ok(());
    }
    let _ = tcp.write_all(&[5, 2, 0, 1, 0, 0, 0, 0, 0, 0]).await;
    Err("Preview requests must use the selected app's virtual HTTP address.".into())
}
