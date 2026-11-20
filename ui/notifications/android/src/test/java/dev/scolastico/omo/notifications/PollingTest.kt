package dev.scolastico.omo.notifications

import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import androidx.work.Configuration
import androidx.work.WorkManager
import androidx.work.testing.SynchronousExecutor
import androidx.work.testing.WorkManagerTestInitHelper
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class PollingTest {
    private lateinit var context: Context
    private lateinit var store: MemoryStore
    private lateinit var engine: PollingEngine
    private val feed = object: Feed {
        var calls = 0
        override fun poll(work: WorkConfig, deliver: (JSONObject) -> Boolean): JSONObject {
            calls++
            return JSONObject().put("more", false)
        }
        override fun recover(work: WorkConfig, replay: Boolean): JSONObject = JSONObject()
    }
    @Before fun setup() {
        context = RuntimeEnvironment.getApplication()
        WorkManagerTestInitHelper.initializeTestWorkManager(context,
            Configuration.Builder().setExecutor(SynchronousExecutor()).build())
        store = MemoryStore()
        engine = PollingEngine(context, store, feed)
        shadowOf(context as android.app.Application).grantPermissions(android.Manifest.permission.POST_NOTIFICATIONS)
        Channels.create(context)
        shadowOf(context.getSystemService(NotificationManager::class.java)).setNotificationsEnabled(true)
    }
    private fun args(mode: String = "background", id: String = "server-a") = JSONObject()
        .put("config", JSONObject().put("id", id).put("notification_mode", mode)
            .put("notifications", true).put("auth", "token").put("remember", true))
        .put("secret", "private-token").put("root", "/private/config")

    @Test fun periodic_work_is_unique_persistent_constrained_and_contains_no_credentials() {
        engine.configure(args())
        engine.configure(args())
        val work = WorkManager.getInstance(context).getWorkInfosForUniqueWork(Scheduler.WORK).get().filter { !it.state.isFinished }
        assertEquals(1,work.size)
        val spec = androidx.work.impl.WorkManagerImpl.getInstance(context).workDatabase.workSpecDao().getWorkSpec(work[0].id.toString())!!
        assertEquals(15 * 60 * 1000L, spec.intervalDuration)
        assertEquals(androidx.work.NetworkType.CONNECTED, spec.constraints.requiredNetworkType)
        assertEquals(setOf("generation"),spec.input.keyValueMap.keys)
        assertFalse(spec.input.toString().contains("private-token"))
        assertEquals("private-token",store.load()!!.secret)
        assertEquals("background",store.load()!!.mode)
    }
    @Test fun switching_modes_and_disconnect_invalidate_obsolete_jobs() {
        engine.configure(args())
        val old = store.load()!!.generation
        engine.configure(args("foreground"))
        val foreground = store.load()!!
        assertTrue(WorkManager.getInstance(context).getWorkInfosForUniqueWork(Scheduler.WORK).get().all { it.state.isFinished })
        val intent = shadowOf(context as android.app.Application).nextStartedService
        assertEquals(PollingService::class.java.name,intent.component!!.className)
        assertEquals(foreground.generation,intent.getStringExtra("generation"))
        assertFalse(intent.extras.toString().contains("private-token"))
        assertFalse(engine.step(old,"background"))
        assertEquals(0,feed.calls)
        engine.configure(args("background"))
        assertFalse(engine.step(foreground.generation,"foreground"))
        engine.configure(null)
        assertNull(store.load())
        assertTrue(WorkManager.getInstance(context).getWorkInfosForUniqueWork(Scheduler.WORK).get().all { it.state.isFinished })
        assertFalse(engine.step(old,"background"))
    }
    @Test fun denied_permission_or_blocked_channel_does_not_call_feed_or_receive_events() {
        engine.configure(args())
        val generation=store.load()!!.generation
        val manager=context.getSystemService(NotificationManager::class.java)
        shadowOf(manager).setNotificationsEnabled(false)
        assertFalse(engine.step(generation,"background"))
        assertEquals(0,feed.calls)
        assertTrue(engine.status().getString("error").contains("notification",true))
        shadowOf(manager).setNotificationsEnabled(true)
        manager.deleteNotificationChannel(Channels.EVENTS)
        manager.createNotificationChannel(android.app.NotificationChannel(Channels.EVENTS,"Off",NotificationManager.IMPORTANCE_NONE))
        assertFalse(engine.step(generation,"background"))
        assertEquals(0,feed.calls)
    }
    @Test fun a_recreated_engine_resumes_persisted_generation_without_a_webview() {
        engine.configure(args())
        val generation=store.load()!!.generation
        val restarted=PollingEngine(context,store,feed)
        assertFalse(restarted.step(generation,"background"))
        assertEquals(1,feed.calls)
        engine.configure(null)
        assertFalse(restarted.step(generation,"background"))
    }
    @Test fun notification_delivery_uses_immutable_connection_scoped_destination_intents() {
        engine.configure(args())
        val work=store.load()!!
        assertTrue(Channels.deliver(context,work,JSONObject().put("id","event:42").put("title","Question").put("body","Choose").put("destination","/7?tab=comments&comment=18")))
        val n=shadowOf(context.getSystemService(NotificationManager::class.java)).allNotifications.single()
        val intent=shadowOf(n.contentIntent).savedIntent
        assertTrue(n.contentIntent.isImmutable)
        assertEquals("server-a",intent.getStringExtra("connection"))
        assertEquals("/7?tab=comments&comment=18",intent.getStringExtra("destination"))
        assertFalse(intent.extras.toString().contains("private-token"))
        assertNotNull(engine.activation(intent))
        engine.configure(args(id="server-b"))
        assertNull(engine.activation(intent))
    }
    @Test fun foreground_service_posts_ongoing_notice_and_stops_on_timeout() {
        engine.configure(args("foreground"))
        val work=store.load()!!
        TestService.engine=engine
        run {
            val controller=Robolectric.buildService(TestService::class.java).create()
            val service=controller.get()
            service.onStartCommand(Intent(context,PollingService::class.java).putExtra("generation",work.generation),0,1)
            val notification=shadowOf(service).lastForegroundNotification
            assertNotNull(notification)
            assertEquals(Channels.SERVICE,notification.channelId)
            assertTrue(notification.flags and android.app.Notification.FLAG_ONGOING_EVENT != 0)
            service.onTimeout(1,android.content.pm.ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
            assertTrue(shadowOf(service).isStoppedBySelf)
            assertTrue(engine.status().getString("error").contains("limit"))
            controller.destroy()
        }
    }
    @Test fun configuration_refuses_unknown_modes_and_nonremembered_authenticated_work() {
        assertThrows(IllegalArgumentException::class.java) {engine.configure(args("unknown"))}
        val arg=args(); arg.getJSONObject("config").put("remember",false)
        assertThrows(IllegalArgumentException::class.java) {engine.configure(arg)}
        assertNull(store.load())
    }
    class TestService: PollingService({ engine }, DirectExecutor()) {
        companion object { lateinit var engine: PollingEngine }
    }
    class DirectExecutor: java.util.concurrent.AbstractExecutorService() {
        private var stopped=false
        override fun execute(task:Runnable) {task.run()}
        override fun shutdown() {stopped=true}
        override fun shutdownNow():MutableList<Runnable> {stopped=true;return mutableListOf()}
        override fun isShutdown()=stopped
        override fun isTerminated()=stopped
        override fun awaitTermination(n:Long,u:java.util.concurrent.TimeUnit)=stopped
    }
    @Test fun encryption_keeps_credentials_out_of_disk_and_survives_store_recreation() {
        val key=javax.crypto.spec.SecretKeySpec(ByteArray(32) {7},"AES")
        val work=WorkConfig(args().getJSONObject("config"),"private-token","/private/config","generation")
        val encrypted=EncryptedWorkStore(context) {key}
        encrypted.save(work)
        val bytes=java.io.File(context.noBackupFilesDir,"notification-work").readBytes()
        assertFalse(String(bytes,Charsets.ISO_8859_1).contains("private-token"))
        val restarted=EncryptedWorkStore(context) {key}
        assertEquals("private-token",restarted.load()!!.secret)
        assertEquals("generation",restarted.load()!!.generation)
        restarted.save(null)
        assertNull(encrypted.load())
    }
    @Test fun mode_changes_keep_taps_for_the_same_connection_usable() {
        engine.configure(args())
        val work=store.load()!!
        val intent=Intent("dev.scolastico.omo.NOTIFICATION").putExtra("connection",work.id)
            .putExtra("generation",work.generation).putExtra("destination","/7")
        engine.configure(args("foreground"))
        assertNotNull(engine.activation(intent))
        engine.configure(null)
        assertNull(engine.activation(intent))
    }
    @Test fun worker_cold_execution_uses_persisted_generation_and_refuses_stale_work() {
        engine.configure(args())
        val generation=store.load()!!.generation
        val factory=object: androidx.work.WorkerFactory() {
            override fun createWorker(ctx:Context,name:String,params:androidx.work.WorkerParameters):androidx.work.ListenableWorker = PollingWorker(ctx,params) {engine}
        }
        val worker=androidx.work.testing.TestWorkerBuilder.from(context,PollingWorker::class.java,SynchronousExecutor())
            .setWorkerFactory(factory).setInputData(androidx.work.workDataOf("generation" to generation)).build()
        assertEquals(androidx.work.ListenableWorker.Result.success(),worker.doWork())
        assertEquals(1,feed.calls)
        engine.configure(args(id="another-server"))
        assertEquals(androidx.work.ListenableWorker.Result.success(),worker.doWork())
        assertEquals(1,feed.calls)
    }
    @Test fun disabling_notifications_preserves_taps_but_cancels_all_polling() {
        engine.configure(args())
        val work=store.load()!!
        val intent=Intent("dev.scolastico.omo.NOTIFICATION").putExtra("connection",work.id)
            .putExtra("generation",work.generation).putExtra("destination","/7")
        val disabled=args();disabled.getJSONObject("config").put("notifications",false)
        engine.configure(disabled)
        assertNotNull(engine.activation(intent))
        assertFalse(engine.step(work.generation,"background"))
        assertEquals(0,feed.calls)
        assertTrue(WorkManager.getInstance(context).getWorkInfosForUniqueWork(Scheduler.WORK).get().all {it.state.isFinished})
        engine.configure(null)
        assertNull(engine.activation(intent))
    }
    @Test fun rejected_foreground_promotion_stops_safely_and_reports_the_failure() {
        engine.configure(args("foreground"))
        RejectingService.engine=engine
        val controller=Robolectric.buildService(RejectingService::class.java).create()
        val service=controller.get()
        try {
            service.onStartCommand(Intent().putExtra("generation",store.load()!!.generation),0,1)
            assertTrue(shadowOf(service).isStoppedBySelf)
            assertTrue(engine.status().getString("error").contains("foreground"))
        } finally {controller.destroy()}
    }
    @Test fun failed_service_store_initialization_stops_without_crashing_or_erasing_work() {
        engine.configure(args("foreground"))
        val work=store.load()!!
        val broken=object: WorkStore {
            override fun load():WorkConfig? {throw java.io.IOException("unavailable")}
            override fun save(work:WorkConfig?) {fail("Must not overwrite an unreadable store")}
        }
        QueuedService.engine=PollingEngine(context,broken,feed)
        QueuedService.queue=QueuedExecutor()
        val controller=Robolectric.buildService(QueuedService::class.java).create()
        val service=controller.get()
        try {
            service.onStartCommand(Intent().putExtra("generation",work.generation),0,1)
            QueuedService.queue.drain()
            assertTrue(shadowOf(service).isStoppedBySelf)
            assertTrue(QueuedService.engine.status().getString("error").isNotEmpty())
            assertEquals(work.generation,store.load()!!.generation)
        } finally {controller.destroy()}
    }
    @Test fun queued_repeated_service_starts_install_only_one_polling_loop() {
        engine.configure(args("foreground"))
        QueuedService.engine=engine;QueuedService.queue=QueuedExecutor()
        val controller=Robolectric.buildService(QueuedService::class.java).create()
        val service=controller.get()
        val intent=Intent().putExtra("generation",store.load()!!.generation)
        try {
            service.onStartCommand(intent,0,1)
            service.onStartCommand(intent,0,2)
            QueuedService.queue.drain()
            shadowOf(android.os.Looper.getMainLooper()).idle()
            QueuedService.queue.drain()
            assertEquals(1,feed.calls)
            shadowOf(android.os.Looper.getMainLooper()).idleFor(java.time.Duration.ofSeconds(5))
            QueuedService.queue.drain()
            assertEquals(2,feed.calls)
        } finally {controller.destroy()}
    }
    @Test fun revoked_android_runtime_permission_blocks_get_polling_without_receiving() {
        engine.configure(args())
        val generation=store.load()!!.generation
        shadowOf(context as android.app.Application).denyPermissions(android.Manifest.permission.POST_NOTIFICATIONS)
        assertFalse(engine.step(generation,"background"))
        assertEquals(0,feed.calls)
        assertTrue(engine.status().getString("error").isNotEmpty())
    }
    class QueuedService: PollingService({engine},queue) {
        companion object {lateinit var engine:PollingEngine;lateinit var queue:QueuedExecutor}
    }
    class RejectingService: PollingService({engine},DirectExecutor(),{throw SecurityException("Rejected by Android")}) {
        companion object {lateinit var engine:PollingEngine}
    }
    class QueuedExecutor: java.util.concurrent.AbstractExecutorService() {
        private val tasks=java.util.ArrayDeque<Runnable>()
        private var stopped=false
        override fun execute(task:Runnable) {check(!stopped);tasks.add(task)}
        fun drain() {while(!tasks.isEmpty()) tasks.removeFirst().run()}
        override fun shutdown() {stopped=true}
        override fun shutdownNow():MutableList<Runnable> {stopped=true;return mutableListOf()}
        override fun isShutdown()=stopped
        override fun isTerminated()=stopped && tasks.isEmpty()
        override fun awaitTermination(n:Long,u:java.util.concurrent.TimeUnit)=isTerminated
    }
    private class MemoryStore: WorkStore {
        private var value: WorkConfig?=null
        override fun load():WorkConfig?=value
        override fun save(work:WorkConfig?) { value=work }
    }
}
