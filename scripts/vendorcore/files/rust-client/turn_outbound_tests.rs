use super::integration_tests::{connect, serve_outbound_test_flow};
use super::*;
use tokio::sync::oneshot;

pub(super) struct WriterGate {
    reached: Mutex<Option<oneshot::Sender<()>>>,
    release: tokio::sync::Mutex<Option<oneshot::Receiver<()>>>,
}

impl WriterGate {
    fn new() -> (Arc<Self>, oneshot::Receiver<()>, oneshot::Sender<()>) {
        let (reached_tx, reached_rx) = oneshot::channel();
        let (release_tx, release_rx) = oneshot::channel();
        (
            Arc::new(Self {
                reached: Mutex::new(Some(reached_tx)),
                release: tokio::sync::Mutex::new(Some(release_rx)),
            }),
            reached_rx,
            release_tx,
        )
    }

    pub(super) async fn pause_after_pop(&self) {
        if let Some(reached) = self
            .reached
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .take()
        {
            let _ = reached.send(());
        }
        let release = self.release.lock().await.take();
        if let Some(release) = release {
            let _ = release.await;
        }
    }
}

async fn allocation(
    pool_size: usize,
) -> (
    Arc<TurnAllocation>,
    Arc<PacketPool>,
    tokio::task::JoinHandle<Vec<Vec<u8>>>,
) {
    let server = Arc::new(UdpSocket::bind("127.0.0.1:0").await.unwrap());
    let server_address = server.local_addr().unwrap();
    let peer: SocketAddr = "127.0.0.1:39011".parse().unwrap();
    let relay: SocketAddr = "127.0.0.1:49011".parse().unwrap();
    let server_task = tokio::spawn(serve_outbound_test_flow(server, peer, relay));
    let pool = PacketPool::new(pool_size);
    let allocation = connect(server_address, peer, pool.clone()).await;
    allocation.prepare_channel().await.unwrap();
    (allocation, pool, server_task)
}

fn install_gate(allocation: &TurnAllocation, gate: Arc<WriterGate>) {
    *allocation
        .outbound_queue
        .as_ref()
        .unwrap()
        .test_writer_gate
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner()) = Some(gate);
}

fn packet(pool: &Arc<PacketPool>, payload: &[u8]) -> PacketBuf {
    let mut packet = pool.acquire();
    packet.read_area()[..payload.len()].copy_from_slice(payload);
    packet.set_read_len(payload.len()).unwrap();
    packet
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn deallocate_waits_for_the_writer_held_packet_before_refresh() {
    let (allocation, pool, server_task) = allocation(1).await;
    let channel = allocation.shared.channel.load(Ordering::Acquire);
    assert_ne!(channel, 0);

    let (gate, reached, release) = WriterGate::new();
    install_gate(&allocation, gate);
    allocation
        .send(packet(&pool, b"last-packet"))
        .await
        .unwrap();
    tokio::time::timeout(Duration::from_secs(2), reached)
        .await
        .unwrap()
        .unwrap();
    assert!(allocation.outbound_queue.as_ref().unwrap().queue.is_empty());

    let flush_started = Instant::now();
    let deallocate = allocation.deallocate();
    tokio::pin!(deallocate);
    let pending = futures::poll!(deallocate.as_mut()).is_pending();
    assert!(flush_started.elapsed() < OUTBOUND_FLUSH_WAIT);
    let closing_before_release = allocation.shared.closing.load(Ordering::Acquire);
    let channel_before_release = allocation.shared.channel.load(Ordering::Acquire);

    release.send(()).unwrap();
    tokio::time::timeout(Duration::from_secs(3), deallocate.as_mut())
        .await
        .unwrap();
    let outbound = tokio::time::timeout(Duration::from_secs(3), server_task)
        .await
        .unwrap()
        .unwrap();

    assert!(
        pending,
        "deallocate completed while the writer held a packet"
    );
    assert!(
        !closing_before_release,
        "deallocate started cleanup before the held packet completed"
    );
    assert_eq!(channel_before_release, channel);
    assert_eq!(outbound, [b"last-packet".to_vec()]);
    assert_eq!(pool.available(), pool.capacity());
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn queued_and_fast_packets_keep_fifo_order_and_return_full_queue_ownership() {
    let (allocation, pool, server_task) = allocation(OUTBOUND_QUEUE_CAPACITY + 3).await;
    let (gate, reached, release) = WriterGate::new();
    install_gate(&allocation, gate);

    let mut first = packet(&pool, b"async-0");
    let first_buffer = first.storage_mut().as_ptr();
    allocation.send(first).await.unwrap();
    tokio::time::timeout(Duration::from_secs(2), reached)
        .await
        .unwrap()
        .unwrap();

    for index in 1..=OUTBOUND_QUEUE_CAPACITY {
        let data = format!("queued-{index}");
        let returned = allocation.try_send_fast(packet(&pool, data.as_bytes()));
        assert!(returned.is_none(), "queue rejected packet {index}");
    }
    let completion = &allocation.outbound_queue.as_ref().unwrap().completion;
    let pending_before_overflow = completion.pending();
    assert_eq!(pending_before_overflow, OUTBOUND_QUEUE_CAPACITY + 1);
    let mut overflow = packet(&pool, b"queue-overflow");
    let overflow_buffer = overflow.storage_mut().as_ptr();
    let mut returned = allocation
        .try_send_fast(overflow)
        .expect("full queue accepted an extra fast packet");
    assert_eq!(returned.as_slice(), b"queue-overflow");
    assert_eq!(returned.storage_mut().as_ptr(), overflow_buffer);
    drop(returned);
    assert_eq!(completion.pending(), pending_before_overflow);

    let mut blocked_packet = packet(&pool, b"async-blocked");
    let blocked_buffer = blocked_packet.storage_mut().as_ptr();
    let send = allocation.send(blocked_packet);
    tokio::pin!(send);
    assert!(futures::poll!(send.as_mut()).is_pending());

    release.send(()).unwrap();
    tokio::time::timeout(Duration::from_secs(3), send.as_mut())
        .await
        .unwrap()
        .unwrap();
    tokio::time::timeout(Duration::from_secs(3), allocation.deallocate())
        .await
        .unwrap();
    let outbound = tokio::time::timeout(Duration::from_secs(3), server_task)
        .await
        .unwrap()
        .unwrap();

    assert_eq!(outbound[0], b"async-0");
    assert_eq!(
        outbound[1..=OUTBOUND_QUEUE_CAPACITY],
        (1..=OUTBOUND_QUEUE_CAPACITY)
            .map(|index| format!("queued-{index}").into_bytes())
            .collect::<Vec<_>>()
    );
    assert_eq!(outbound.last().unwrap(), b"async-blocked");
    assert_eq!(completion.pending(), 0);
    assert_eq!(pool.available(), pool.capacity());
    let mut recovered = Vec::with_capacity(pool.capacity());
    while let Some(packet) = pool.try_acquire() {
        recovered.push(packet);
    }
    let recovered_buffers = recovered
        .iter_mut()
        .map(|packet| packet.storage_mut().as_ptr())
        .collect::<Vec<_>>();
    assert!(recovered_buffers.contains(&first_buffer));
    assert!(recovered_buffers.contains(&blocked_buffer));
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn writer_returns_lease_when_channel_data_encoding_fails() {
    let (allocation, pool, server_task) = allocation(1).await;
    let mut held_packet = packet(&pool, b"invalid-channel");
    let held_buffer = held_packet.storage_mut().as_ptr();
    let (gate, reached, release) = WriterGate::new();
    install_gate(&allocation, gate);
    allocation.send(held_packet).await.unwrap();
    tokio::time::timeout(Duration::from_secs(2), reached)
        .await
        .unwrap()
        .unwrap();

    allocation.shared.channel.store(0x3fff, Ordering::Release);
    release.send(()).unwrap();
    tokio::time::timeout(Duration::from_secs(3), allocation.deallocate())
        .await
        .unwrap();
    let outbound = tokio::time::timeout(Duration::from_secs(3), server_task)
        .await
        .unwrap()
        .unwrap();

    assert!(outbound.is_empty());
    assert_eq!(
        allocation
            .outbound_queue
            .as_ref()
            .unwrap()
            .completion
            .pending(),
        0
    );
    assert_eq!(pool.available(), pool.capacity());
    let mut returned = pool.try_acquire().unwrap();
    assert_eq!(returned.storage_mut().as_ptr(), held_buffer);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn deallocate_keeps_its_existing_deadline_for_held_and_queued_packets() {
    let (allocation, pool, server_task) = allocation(OUTBOUND_QUEUE_CAPACITY + 1).await;
    let (gate, reached, release) = WriterGate::new();
    install_gate(&allocation, gate);
    let mut held_packet = packet(&pool, b"deadline-packet");
    let held_buffer = held_packet.storage_mut().as_ptr();
    allocation.send(held_packet).await.unwrap();
    tokio::time::timeout(Duration::from_secs(2), reached)
        .await
        .unwrap()
        .unwrap();

    let mut queued_buffers = Vec::with_capacity(OUTBOUND_QUEUE_CAPACITY);
    for index in 0..OUTBOUND_QUEUE_CAPACITY {
        let data = format!("queued-{index}");
        let mut queued_packet = packet(&pool, data.as_bytes());
        queued_buffers.push(queued_packet.storage_mut().as_ptr());
        assert!(allocation.try_send_fast(queued_packet).is_none());
    }

    let start = Instant::now();
    tokio::time::timeout(Duration::from_secs(3), allocation.deallocate())
        .await
        .unwrap();
    assert!(start.elapsed() >= OUTBOUND_FLUSH_WAIT);
    assert!(start.elapsed() < OUTBOUND_FLUSH_WAIT + Duration::from_secs(2));
    assert_eq!(pool.available(), pool.capacity());
    let _ = release.send(());
    let outbound = tokio::time::timeout(Duration::from_secs(3), server_task)
        .await
        .unwrap()
        .unwrap();
    assert!(outbound.is_empty());

    let returned = allocation
        .try_send_fast(packet(&pool, b"closed-allocation"))
        .expect("closed allocation accepted a fast packet");
    drop(returned);

    let queue = allocation.outbound_queue.as_ref().unwrap();
    let mut closed_packet = packet(&pool, b"closed-queue");
    let closed_buffer = closed_packet.storage_mut().as_ptr();
    let mut returned = queue
        .try_enqueue(closed_packet)
        .expect_err("closed queue accepted a packet");
    assert_eq!(returned.storage_mut().as_ptr(), closed_buffer);
    assert_eq!(queue.completion.pending(), 0);
    drop(returned);

    let mut recovered = Vec::with_capacity(pool.capacity());
    while let Some(packet) = pool.try_acquire() {
        recovered.push(packet);
    }
    assert_eq!(recovered.len(), pool.capacity());
    let recovered_buffers = recovered
        .iter_mut()
        .map(|packet| packet.storage_mut().as_ptr())
        .collect::<Vec<_>>();
    assert!(recovered_buffers.contains(&held_buffer));
    for buffer in queued_buffers {
        assert!(recovered_buffers.contains(&buffer));
    }
}
