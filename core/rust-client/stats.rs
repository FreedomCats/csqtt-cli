use crate::events::Events;
use std::sync::{
    Arc,
    atomic::{AtomicI32, AtomicI64, Ordering},
};
use tokio_util::sync::CancellationToken;

#[derive(Default)]
pub struct Stats {
    /// Tunnel uplink only: packets `Dispatcher::dispatch` accepted for the tunnel.
    pub total_bytes_up: AtomicI64,
    /// Tunnel downlink only: worker packets and reassembled fragments. A packet
    /// is counted once, when it enters the inbound queue; a later queue eviction
    /// or a failed device write does not adjust the counter.
    pub total_bytes_down: AtomicI64,
    /// Direct-path uplink only: packets accepted by `DirectRelay::try_handle`.
    pub direct_bytes_up: AtomicI64,
    /// Direct-path downlink only: packets produced by the direct stack, counted
    /// once at the same inbound-queue point as the tunnel downlink.
    pub direct_bytes_down: AtomicI64,
    pub active_connections: AtomicI32,
}

impl Stats {
    pub async fn run(self: Arc<Self>, events: Events, cancel: CancellationToken) {
        let mut interval = tokio::time::interval(std::time::Duration::from_millis(500));
        interval.tick().await;
        let mut log_counter = 0u32;
        loop {
            tokio::select! {
                _ = cancel.cancelled() => return,
                _ = interval.tick() => {
                    let active = self.active_connections.load(Ordering::Relaxed);
                    let up = self.total_bytes_up.load(Ordering::Relaxed);
                    let down = self.total_bytes_down.load(Ordering::Relaxed);
                    let direct_up = self.direct_bytes_up.load(Ordering::Relaxed);
                    let direct_down = self.direct_bytes_down.load(Ordering::Relaxed);
                    log_counter += 1;
                    if log_counter >= 6 {
                        log_counter = 0;
                        let total_mb = (up + down + direct_up + direct_down) as f64 / (1024.0 * 1024.0);
                        crate::log_error!("[СТАТИСТИКА] Активных: {active} | Трафик: {total_mb:.2} МБ");
                    }
                    events.stats(active, up, down, direct_up, direct_down);
                }
            }
        }
    }
}
